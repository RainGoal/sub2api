package service

import "time"

type AccountGroup struct {
	AccountID int64
	GroupID   int64
	Priority  int
	CreatedAt time.Time

	Account *Account
	Group   *Group
}

// AccountGroupPriorityInput sets the priority of an account within one group.
type AccountGroupPriorityInput struct {
	GroupID  int64 `json:"group_id"`
	Priority int   `json:"priority"`
}
