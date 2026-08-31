package videoprovider

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// Both provider adapters must expose the same internal task-state vocabulary
// even though their wire protocols use different names for the task ID and
// terminal states.  The public response mapper builds on this invariant.
func TestSeedanceProviderStatusAliasesShareInternalStates(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		want Status
	}{
		{name: "queued", raw: "queued", want: StatusPending},
		{name: "pending", raw: "pending", want: StatusPending},
		{name: "running", raw: "running", want: StatusRunning},
		{name: "in progress", raw: "in_progress", want: StatusRunning},
		{name: "processing", raw: "processing", want: StatusRunning},
		{name: "cancellation requested", raw: "cancel_requested", want: StatusRunning},
		{name: "settling", raw: "settling", want: StatusSettling},
		{name: "completed", raw: "completed", want: StatusCompleted},
		{name: "succeeded", raw: "succeeded", want: StatusCompleted},
		{name: "success", raw: "success", want: StatusCompleted},
		{name: "failed", raw: "failed", want: StatusFailed},
		{name: "error", raw: "error", want: StatusFailed},
		{name: "canceled", raw: "canceled", want: StatusCanceled},
		{name: "cancelled", raw: "cancelled", want: StatusCanceled},
	}

	providers := []struct {
		name   string
		driver Driver
		idKey  string
		taskID string
	}{
		{name: "bblabu", driver: bblabuDriver{}, idKey: "task_id", taskID: "task-bblabu"},
		{name: "fflink", driver: fflinkDriver{}, idKey: "job_id", taskID: "task-fflink"},
	}

	for _, provider := range providers {
		provider := provider
		t.Run(provider.name, func(t *testing.T) {
			t.Parallel()
			for _, tc := range cases {
				tc := tc
				t.Run(tc.name, func(t *testing.T) {
					body, err := json.Marshal(map[string]string{
						provider.idKey: provider.taskID,
						"status":       tc.raw,
					})
					require.NoError(t, err)

					task, err := provider.driver.ParseTask(body, "")
					require.NoError(t, err)
					require.Equal(t, provider.taskID, task.ID)
					require.Equal(t, tc.want, task.Status)
				})
			}
		})
	}
}

func TestSeedanceProviderParseTaskUsesFallbackIDOnlyWhenBodyOmitsID(t *testing.T) {
	t.Parallel()

	for _, provider := range []struct {
		name   string
		driver Driver
	}{
		{name: "bblabu", driver: bblabuDriver{}},
		{name: "fflink", driver: fflinkDriver{}},
	} {
		provider := provider
		t.Run(provider.name, func(t *testing.T) {
			t.Parallel()
			task, err := provider.driver.ParseTask([]byte(`{"status":"pending"}`), "fallback-task")
			require.NoError(t, err)
			require.Equal(t, "fallback-task", task.ID)
			require.Equal(t, StatusPending, task.Status)

			_, err = provider.driver.ParseTask([]byte(`{"status":`), "")
			require.Error(t, err)
		})
	}
}
