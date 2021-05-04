package vault

import (
	"context"
	"testing"

	"github.com/hashicorp/boundary/internal/credential/vault/store"
	"github.com/hashicorp/boundary/internal/db"
	"github.com/hashicorp/boundary/internal/iam"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMethodType(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)
	assert.IsType(MethodGet, MethodPost)
	assert.IsType(MethodPost, MethodGet)
}

func TestCredentialLibrary_New(t *testing.T) {
	t.Parallel()
	conn, _ := db.TestSetup(t, "postgres")
	wrapper := db.TestWrapper(t)
	rw := db.New(conn)

	_, prj := iam.TestScopes(t, iam.TestRepo(t, conn, wrapper))
	cs := TestCredentialStores(t, conn, wrapper, prj.PublicId, 1)[0]

	type args struct {
		storeId   string
		vaultPath string
		opts      []Option
	}

	tests := []struct {
		name    string
		args    args
		want    *CredentialLibrary
		wantErr bool
	}{
		{
			name: "missing-store-id",
			args: args{
				storeId:   "",
				vaultPath: "vault/path",
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "missing-vault-path",
			args: args{
				storeId: cs.PublicId,
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "valid-no-options",
			args: args{
				storeId:   cs.PublicId,
				vaultPath: "vault/path",
			},
			want: &CredentialLibrary{
				CredentialLibrary: &store.CredentialLibrary{
					StoreId:    cs.PublicId,
					HttpMethod: "GET",
					VaultPath:  "vault/path",
				},
			},
		},
		{
			name: "valid-with-name",
			args: args{
				storeId:   cs.PublicId,
				vaultPath: "vault/path",
				opts: []Option{
					WithName("test-name"),
				},
			},
			want: &CredentialLibrary{
				CredentialLibrary: &store.CredentialLibrary{
					StoreId:    cs.PublicId,
					HttpMethod: "GET",
					VaultPath:  "vault/path",
					Name:       "test-name",
				},
			},
		},
		{
			name: "valid-with-description",
			args: args{
				storeId:   cs.PublicId,
				vaultPath: "vault/path",
				opts: []Option{
					WithDescription("test-description"),
				},
			},
			want: &CredentialLibrary{
				CredentialLibrary: &store.CredentialLibrary{
					StoreId:     cs.PublicId,
					HttpMethod:  "GET",
					VaultPath:   "vault/path",
					Description: "test-description",
				},
			},
		},
		{
			name: "valid-with-post-method",
			args: args{
				storeId:   cs.PublicId,
				vaultPath: "vault/path",
				opts: []Option{
					WithMethod(MethodPost),
				},
			},
			want: &CredentialLibrary{
				CredentialLibrary: &store.CredentialLibrary{
					StoreId:    cs.PublicId,
					HttpMethod: "POST",
					VaultPath:  "vault/path",
				},
			},
		},
		{
			name: "valid-with-post-method-and-body",
			args: args{
				storeId:   cs.PublicId,
				vaultPath: "vault/path",
				opts: []Option{
					WithMethod(MethodPost),
					WithRequestBody("body"),
				},
			},
			want: &CredentialLibrary{
				CredentialLibrary: &store.CredentialLibrary{
					StoreId:         cs.PublicId,
					HttpMethod:      "POST",
					VaultPath:       "vault/path",
					HttpRequestBody: "body",
				},
			},
		},
		{
			name: "invalid-get-method-with-body",
			args: args{
				storeId:   cs.PublicId,
				vaultPath: "vault/path",
				opts: []Option{
					WithRequestBody("body"),
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			assert, require := assert.New(t), require.New(t)
			ctx := context.Background()
			got, err := NewCredentialLibrary(tt.args.storeId, tt.args.vaultPath, tt.args.opts...)
			if tt.wantErr {
				assert.Error(err)
				require.Nil(got)
				return
			}
			require.NoError(err)
			require.NotNil(got)

			assert.Emptyf(got.PublicId, "PublicId set")
			assert.Equal(tt.want, got)

			id, err := newCredentialLibraryId()
			assert.NoError(err)

			tt.want.PublicId = id
			got.PublicId = id

			err2 := rw.Create(ctx, got)
			assert.NoError(err2)
		})
	}
}
