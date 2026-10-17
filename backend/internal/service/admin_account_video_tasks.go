package service

import (
	"context"
	"maps"
	"net/http"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrVideoTaskCredentialsProtected = infraerrors.New(http.StatusConflict, "VIDEO_TASK_CREDENTIALS_PROTECTED",
	"Account credentials are needed by active video tasks or the seven-day completed-task retrieval window; create a new account to rotate the key")

func protectVideoTaskAccount(ctx context.Context, repo AccountRepository, account *Account, deleting bool, credentials map[string]any, accountType string) error {
	if account == nil || !account.IsSeedance() {
		return nil
	}
	changesIdentity := deleting || (accountType != "" && accountType != account.Type)
	if !changesIdentity && len(credentials) > 0 {
		merged := MergePreservingSensitiveCreds(account.Credentials, credentials)
		current, _ := account.Credentials["api_key"].(string)
		replacement, _ := merged["api_key"].(string)
		changesIdentity = current != replacement
	}
	if !changesIdentity {
		return nil
	}
	dependencies, ok := repo.(SeedanceVideoTaskAccountDependencyRepository)
	if !ok {
		return nil
	} // Compatibility for lightweight repositories.
	protected, err := dependencies.HasUnfinishedForAccount(ctx, account.ID)
	if err != nil {
		return err
	}
	if protected {
		return ErrVideoTaskCredentialsProtected
	}
	return nil
}

func validateVideoTaskBulkUpdate(account *Account, credentials, extra map[string]any) error {
	if account == nil || !account.IsSeedance() {
		return nil
	}
	copy := *account
	copy.Credentials = maps.Clone(account.Credentials)
	if copy.Credentials == nil {
		copy.Credentials = make(map[string]any)
	}
	for key, value := range credentials {
		copy.Credentials[key] = value
	}
	copy.Extra = maps.Clone(account.Extra)
	if copy.Extra == nil {
		copy.Extra = make(map[string]any)
	}
	for key, value := range extra {
		copy.Extra[key] = value
	}
	if len(credentials) > 0 {
		if err := validateSeedanceAccountCredentials(copy.Platform, copy.Type, copy.Credentials); err != nil {
			return err
		}
	}
	return ValidateVideoTasksAccount(&copy)
}
