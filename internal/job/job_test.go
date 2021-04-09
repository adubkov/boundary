package job

import (
	"testing"

	"github.com/hashicorp/boundary/internal/job/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJob_SetTableName(t *testing.T) {
	defaultTableName := "job"
	tests := []struct {
		name        string
		initialName string
		setNameTo   string
		want        string
	}{
		{
			name:        "new-name",
			initialName: "",
			setNameTo:   "new-name",
			want:        "new-name",
		},
		{
			name:        "reset to default",
			initialName: "initial",
			setNameTo:   "",
			want:        defaultTableName,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert, require := assert.New(t), require.New(t)
			def := &Job{
				Job: &store.Job{},
			}
			require.Equal(defaultTableName, def.TableName())
			s := &Job{
				Job:       &store.Job{},
				tableName: tt.initialName,
			}
			s.SetTableName(tt.setNameTo)
			assert.Equal(tt.want, s.TableName())
		})
	}
}
