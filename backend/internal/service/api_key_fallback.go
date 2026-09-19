package service

import (
	"context"
	"fmt"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrAPIKeyFallbackInvalid = infraerrors.BadRequest("API_KEY_FALLBACK_INVALID", "fallback requires distinct active standard groups on the same supported text platform without group fallback routes")

// ResolveFallbackGroup validates fresh group and user state only when fallback is
// actually needed. The cached key stores an ID, never a fallback group snapshot.
// A nil result without error means fallback is not configured.
func (s *APIKeyService) ResolveFallbackGroup(ctx context.Context, apiKey *APIKey) (*Group, error) {
	if s == nil || apiKey == nil || apiKey.FallbackGroupID == nil {
		return nil, nil
	}
	group, err := s.validateFallbackBinding(ctx, apiKey.UserID, apiKey.GroupID, apiKey.FallbackGroupID)
	if err != nil {
		return nil, err
	}
	// A group changed after authentication must not switch the protocol handled
	// by the current request. Its next request will refresh through normal auth.
	if apiKey.Group != nil && group.Platform != apiKey.Group.Platform {
		return nil, ErrAPIKeyFallbackInvalid
	}
	return group, nil
}

func (s *APIKeyService) validateFallbackBinding(ctx context.Context, userID int64, primaryID, fallbackID *int64) (*Group, error) {
	if primaryID == nil || fallbackID == nil || *primaryID <= 0 || *fallbackID <= 0 || *primaryID == *fallbackID {
		return nil, ErrAPIKeyFallbackInvalid
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get fallback user: %w", err)
	}
	if user == nil || !user.IsActive() {
		return nil, ErrGroupNotAllowed
	}
	primary, err := s.groupRepo.GetByIDLite(ctx, *primaryID)
	if err != nil {
		return nil, fmt.Errorf("get primary group: %w", err)
	}
	fallback, err := s.groupRepo.GetByIDLite(ctx, *fallbackID)
	if err != nil {
		return nil, fmt.Errorf("get fallback group: %w", err)
	}
	if !apiKeyFallbackGroupEligible(primary) || !apiKeyFallbackGroupEligible(fallback) || primary.Platform != fallback.Platform {
		return nil, ErrAPIKeyFallbackInvalid
	}
	if !user.CanBindGroup(primary.ID, primary.IsExclusive) || !user.CanBindGroup(fallback.ID, fallback.IsExclusive) {
		return nil, ErrGroupNotAllowed
	}
	return fallback, nil
}

func apiKeyFallbackGroupEligible(group *Group) bool {
	return group != nil && group.IsActive() && group.SubscriptionType == SubscriptionTypeStandard &&
		(group.Platform == PlatformOpenAI || group.Platform == PlatformAnthropic) &&
		group.FallbackGroupID == nil && group.FallbackGroupIDOnInvalidRequest == nil
}

// Keep unchanged configuration editable. Changing either group requires clearing
// the fallback or validating the new pairing.
func (s *APIKeyService) applyFallbackUpdate(ctx context.Context, key *APIKey, req UpdateAPIKeyRequest) error {
	fallbackID := key.FallbackGroupID
	if req.FallbackGroupIDSet {
		fallbackID = req.FallbackGroupID
	}
	primaryID := key.GroupID
	if req.GroupID != nil {
		primaryID = req.GroupID
	}
	changed := !sameAPIKeyGroupID(fallbackID, key.FallbackGroupID) || !sameAPIKeyGroupID(primaryID, key.GroupID)
	if fallbackID != nil && changed {
		if _, err := s.validateFallbackBinding(ctx, key.UserID, primaryID, fallbackID); err != nil {
			return err
		}
	}
	key.FallbackGroupID = fallbackID
	return nil
}

func sameAPIKeyGroupID(a, b *int64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
