package oidc

const (
	acctUpsertQuery = `
		insert into auth_oidc_account
			(%s)
		values
			(%s)
		on conflict on constraint
			auth_oidc_account_auth_method_id_issuer_subject_uq
		do update set
			%s
		returning public_id, version
	`

	groupMempershipUpsertQuery = `
        insert into iam_group_member_user (group_id, member_id)
            select
                public_id,
                (
                    select iam_user_id
                    from auth_account
                    where public_id = '%s'
                ) as member_id
            from iam_group
            where name in ('%s')
        on conflict do nothing;
    `
)
