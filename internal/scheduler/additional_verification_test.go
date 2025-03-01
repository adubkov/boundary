package scheduler

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/hashicorp/boundary/internal/db"
	"github.com/hashicorp/boundary/internal/iam"
	"github.com/hashicorp/boundary/internal/kms"
	"github.com/hashicorp/boundary/internal/scheduler/job"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchedulerWorkflow(t *testing.T) {
	t.Parallel()
	assert, require := assert.New(t), require.New(t)
	conn, _ := db.TestSetup(t, "postgres")
	wrapper := db.TestWrapper(t)
	iam.TestRepo(t, conn, wrapper)

	server := testController(t, conn, wrapper)
	sched := testScheduler(t, conn, wrapper, server.PrivateId, WithRunJobsLimit(10), WithRunJobsInterval(time.Second))

	job1Ch := make(chan error)
	job1Ready := make(chan struct{})
	fn1 := func(_ context.Context) error {
		job1Ready <- struct{}{}
		return <-job1Ch
	}
	tj1 := testJob{name: "name1", description: "desc", fn: fn1, nextRunIn: time.Hour}
	err := sched.RegisterJob(context.Background(), tj1)
	require.NoError(err)

	job2Ch := make(chan error)
	job2Ready := make(chan struct{})
	fn2 := func(_ context.Context) error {
		job2Ready <- struct{}{}
		return <-job2Ch
	}
	tj2 := testJob{name: "name2", description: "desc", fn: fn2, nextRunIn: time.Hour}
	err = sched.RegisterJob(context.Background(), tj2)
	require.NoError(err)

	err = sched.Start(context.Background())
	require.NoError(err)

	// Wait for scheduler to run both jobs
	<-job1Ready
	<-job2Ready

	assert.Equal(mapLen(sched.runningJobs), 2)

	// Fail first job, complete second job
	job1Ch <- fmt.Errorf("failure")
	job2Ch <- nil

	// Scheduler should only try and run job1 again as job2 was successful
	<-job1Ready

	require.Equal(mapLen(sched.runningJobs), 1)

	// Complete job 1
	job1Ch <- nil

	// Update job2 to run again
	err = sched.UpdateJobNextRun(context.Background(), tj2.name, 0)
	require.NoError(err)
	<-job2Ready

	require.Equal(mapLen(sched.runningJobs), 1)

	// Complete job 2
	job2Ch <- nil
}

func TestSchedulerCancelCtx(t *testing.T) {
	t.Parallel()
	assert, require := assert.New(t), require.New(t)
	conn, _ := db.TestSetup(t, "postgres")
	wrapper := db.TestWrapper(t)
	iam.TestRepo(t, conn, wrapper)

	server := testController(t, conn, wrapper)
	sched := testScheduler(t, conn, wrapper, server.PrivateId, WithRunJobsLimit(10), WithRunJobsInterval(time.Second))

	fn, jobReady, jobDone := testJobFn()
	tj := testJob{name: "name", description: "desc", fn: fn, nextRunIn: time.Hour}
	err := sched.RegisterJob(context.Background(), tj)
	require.NoError(err)

	baseCtx, baseCnl := context.WithCancel(context.Background())
	err = sched.Start(baseCtx)
	require.NoError(err)

	// Wait for scheduler to run job
	<-jobReady

	assert.Equal(mapLen(sched.runningJobs), 1)

	// Yield processor
	runtime.Gosched()

	// Verify job is not done
	select {
	case <-jobDone:
		t.Fatal("expected job to be blocking on context")
	default:
	}

	// Cancel the base context and all job context's should be cancelled and exit
	baseCnl()
	<-jobDone
}

func TestSchedulerInterruptedCancelCtx(t *testing.T) {
	t.Parallel()
	assert, require := assert.New(t), require.New(t)
	conn, _ := db.TestSetup(t, "postgres")
	wrapper := db.TestWrapper(t)
	rw := db.New(conn)
	kmsCache := kms.TestKms(t, conn, wrapper)
	iam.TestRepo(t, conn, wrapper)

	server := testController(t, conn, wrapper)
	sched := testScheduler(t, conn, wrapper, server.PrivateId, WithRunJobsLimit(10), WithRunJobsInterval(time.Second), WithMonitorInterval(time.Second))

	fn, job1Ready, job1Done := testJobFn()
	tj1 := testJob{name: "name1", description: "desc", fn: fn, nextRunIn: time.Hour}
	err := sched.RegisterJob(context.Background(), tj1)
	require.NoError(err)

	fn, job2Ready, job2Done := testJobFn()
	tj2 := testJob{name: "name2", description: "desc", fn: fn, nextRunIn: time.Hour}
	err = sched.RegisterJob(context.Background(), tj2)
	require.NoError(err)

	baseCtx, baseCnl := context.WithCancel(context.Background())
	defer baseCnl()
	err = sched.Start(baseCtx)
	require.NoError(err)

	// Wait for scheduler to run both job
	<-job1Ready
	<-job2Ready

	require.Equal(mapLen(sched.runningJobs), 2)
	runJob, ok := sched.runningJobs.Load(tj1.name)
	require.True(ok)
	run1Id := runJob.(*runningJob).runId
	runJob, ok = sched.runningJobs.Load(tj2.name)
	require.True(ok)
	run2Id := runJob.(*runningJob).runId

	// Yield processor
	runtime.Gosched()

	// Verify job 1 is not done
	select {
	case <-job1Done:
		t.Fatal("expected job 1 to be blocking on context")
	default:
	}

	// Verify job 2 is not done
	select {
	case <-job2Done:
		t.Fatal("expected job 2 to be blocking on context")
	default:
	}

	// Interrupt job 1 run to cause monitor loop to trigger cancel
	repo, err := job.NewRepository(rw, rw, kmsCache)
	require.NoError(err)
	run, err := repo.LookupRun(context.Background(), run1Id)
	require.NoError(err)
	run.Status = string(job.Interrupted)
	rowsUpdated, err := rw.Update(context.Background(), run, []string{"Status"}, nil)
	require.NoError(err)
	assert.Equal(1, rowsUpdated)

	// Once monitor cancels context the job should exit
	<-job1Done

	// Yield processor
	runtime.Gosched()

	// Verify job 2 is not done
	select {
	case <-job2Done:
		t.Fatal("expected job 2 to be blocking on context")
	default:
	}

	// Interrupt job 2 run to cause monitor loop to trigger cancel
	repo, err = job.NewRepository(rw, rw, kmsCache)
	require.NoError(err)
	run, err = repo.LookupRun(context.Background(), run2Id)
	require.NoError(err)
	run.Status = string(job.Interrupted)
	rowsUpdated, err = rw.Update(context.Background(), run, []string{"Status"}, nil)
	require.NoError(err)
	assert.Equal(1, rowsUpdated)

	// Once monitor cancels context the job should exit
	<-job2Done
}

func TestSchedulerJobProgress(t *testing.T) {
	t.Parallel()
	assert, require := assert.New(t), require.New(t)
	conn, _ := db.TestSetup(t, "postgres")
	wrapper := db.TestWrapper(t)
	rw := db.New(conn)
	kmsCache := kms.TestKms(t, conn, wrapper)
	iam.TestRepo(t, conn, wrapper)

	server := testController(t, conn, wrapper)
	sched := testScheduler(t, conn, wrapper, server.PrivateId, WithRunJobsLimit(10), WithRunJobsInterval(time.Second), WithMonitorInterval(time.Second))

	jobReady := make(chan struct{})
	fn := func(ctx context.Context) error {
		jobReady <- struct{}{}
		<-ctx.Done()
		return nil
	}

	statusRequest := make(chan struct{})
	jobStatus := make(chan JobStatus)
	status := func() JobStatus {
		statusRequest <- struct{}{}
		return <-jobStatus
	}
	tj := testJob{name: "name", description: "desc", fn: fn, statusFn: status, nextRunIn: time.Hour}
	err := sched.RegisterJob(context.Background(), tj)
	require.NoError(err)

	baseCtx, baseCnl := context.WithCancel(context.Background())
	err = sched.Start(baseCtx)
	require.NoError(err)

	// Wait for scheduler to run job
	<-jobReady

	require.Equal(mapLen(sched.runningJobs), 1)
	runJob, ok := sched.runningJobs.Load(tj.name)
	require.True(ok)
	runId := runJob.(*runningJob).runId

	// Wait for scheduler to query for job status
	<-statusRequest

	// Send progress to monitor loop to persist
	jobStatus <- JobStatus{Total: 10, Completed: 0}

	// Wait for scheduler to query for job status before verifying previous results
	<-statusRequest

	repo, err := job.NewRepository(rw, rw, kmsCache)
	require.NoError(err)
	run, err := repo.LookupRun(context.Background(), runId)
	require.NoError(err)
	assert.Equal(string(job.Running), run.Status)
	assert.Equal(uint32(10), run.TotalCount)
	assert.Equal(uint32(0), run.CompletedCount)

	// Send progress to monitor loop to persist
	jobStatus <- JobStatus{Total: 20, Completed: 10}

	// Wait for scheduler to query for job status before verifying previous results
	<-statusRequest

	run, err = repo.LookupRun(context.Background(), runId)
	require.NoError(err)
	assert.Equal(string(job.Running), run.Status)
	assert.Equal(uint32(20), run.TotalCount)
	assert.Equal(uint32(10), run.CompletedCount)

	// Send progress to monitor loop to persist
	jobStatus <- JobStatus{Total: 10, Completed: 20}

	// Wait for scheduler to query for job status before verifying previous results
	<-statusRequest

	// Previous job status was invalid and should not have been persisted
	run, err = repo.LookupRun(context.Background(), runId)
	require.NoError(err)
	assert.Equal(string(job.Running), run.Status)
	assert.Equal(uint32(20), run.TotalCount)
	assert.Equal(uint32(10), run.CompletedCount)

	baseCnl()
	// unblock goroutines waiting on channels
	jobStatus <- JobStatus{}
}

func TestSchedulerMonitorLoop(t *testing.T) {
	t.Parallel()
	assert, require := assert.New(t), require.New(t)
	conn, _ := db.TestSetup(t, "postgres")
	wrapper := db.TestWrapper(t)
	rw := db.New(conn)
	kmsCache := kms.TestKms(t, conn, wrapper)
	iam.TestRepo(t, conn, wrapper)

	server := testController(t, conn, wrapper)
	sched := testScheduler(t, conn, wrapper, server.PrivateId, WithRunJobsLimit(10), WithInterruptThreshold(time.Second), WithRunJobsInterval(time.Second), WithMonitorInterval(time.Second))

	jobReady := make(chan struct{})
	jobDone := make(chan struct{})
	fn := func(ctx context.Context) error {
		jobReady <- struct{}{}
		<-ctx.Done()
		jobDone <- struct{}{}
		return nil
	}
	tj := testJob{name: "name", description: "desc", fn: fn, nextRunIn: time.Hour}
	err := sched.RegisterJob(context.Background(), tj)
	require.NoError(err)

	baseCtx, baseCnl := context.WithCancel(context.Background())
	defer baseCnl()
	err = sched.Start(baseCtx)
	require.NoError(err)

	// Wait for scheduler to run job
	<-jobReady

	require.Equal(mapLen(sched.runningJobs), 1)
	runJob, ok := sched.runningJobs.Load(tj.name)
	require.True(ok)
	runId := runJob.(*runningJob).runId

	// Wait for scheduler to interrupt job
	<-jobDone

	repo, err := job.NewRepository(rw, rw, kmsCache)
	require.NoError(err)
	run, err := repo.LookupRun(context.Background(), runId)
	require.NoError(err)
	assert.Equal(string(job.Interrupted), run.Status)
}
