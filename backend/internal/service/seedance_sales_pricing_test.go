package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/videoprovider"
	"github.com/stretchr/testify/require"
)

func seedanceSalesTestPrice(value float64) *float64 { return &value }

func seedanceSalesTestGroup() *Group {
	return &Group{ID: 8, Platform: PlatformSeedance, VideoModelPrices: map[string]map[string]float64{
		videoprovider.ModelSeedance20: {"720p": 0.1},
	}}
}

func seedanceSalesTestService(channel *Channel) *ChannelService {
	svc := &ChannelService{}
	var channels []Channel
	if channel != nil {
		channels = []Channel{*channel}
		channels[0].GroupIDs = []int64{8}
	}
	svc.cache.Store(populateChannelCache(channels, map[int64]string{8: PlatformSeedance}))
	return svc
}

func TestSeedanceSalesPriceOwnership(t *testing.T) {
	price := ChannelModelPricing{Platform: PlatformSeedance, Models: []string{videoprovider.ModelSeedance20},
		BillingMode: BillingModeVideo, Intervals: []PricingInterval{{TierLabel: "720p", PerRequestPrice: seedanceSalesTestPrice(0.2)}}}
	for _, tc := range []struct {
		name    string
		channel *Channel
		want    *float64
		source  string
	}{
		{"channel overrides legacy", &Channel{Status: StatusActive, ModelPricing: []ChannelModelPricing{price}}, seedanceSalesTestPrice(0.2), PricingSourceChannel},
		{"removed prices stay removed", &Channel{Status: StatusActive, FeaturesConfig: map[string]any{seedanceSalesPricingKey: true}}, nil, PricingSourceChannel},
		{"disabled channel never revives legacy", &Channel{Status: "disabled", ModelPricing: []ChannelModelPricing{price}}, nil, PricingSourceChannel},
		{"disabled empty owned channel", &Channel{Status: "disabled", FeaturesConfig: map[string]any{seedanceSalesPricingKey: true}}, nil, PricingSourceChannel},
		{"unmigrated group", nil, seedanceSalesTestPrice(0.1), PricingSourceGroup},
		{"unmigrated channel", &Channel{Status: StatusActive}, seedanceSalesTestPrice(0.1), PricingSourceGroup},
		{"legacy non-video rows do not take ownership", &Channel{Status: StatusActive, ModelPricing: []ChannelModelPricing{
			{Platform: PlatformSeedance, Models: []string{videoprovider.ModelSeedance20}, BillingMode: BillingModeToken, InputPrice: seedanceSalesTestPrice(1)},
		}}, seedanceSalesTestPrice(0.1), PricingSourceGroup},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, source, err := ResolveSeedanceSalesPrice(context.Background(), seedanceSalesTestService(tc.channel), seedanceSalesTestGroup(), videoprovider.ModelSeedance20, "720p")
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.source, source)
		})
	}
}

func TestSeedanceSalesPriceModelAndResolution(t *testing.T) {
	svc := seedanceSalesTestService(&Channel{Status: StatusActive, ModelPricing: []ChannelModelPricing{
		{Platform: PlatformSeedance, Models: []string{"bytedance/seedance-2.5"}, BillingMode: BillingModeVideo,
			PerRequestPrice: seedanceSalesTestPrice(0.3), Intervals: []PricingInterval{{TierLabel: "720p", PerRequestPrice: seedanceSalesTestPrice(0)}}},
	}})
	for _, tc := range []struct {
		name, model, tier string
		want              *float64
		err               bool
	}{
		{"aliases share prices", " SEEDANCE-2.5 ", "720P", seedanceSalesTestPrice(0), false},
		{"default per-second price", "bytedance/seedance-2.5", "480p", seedanceSalesTestPrice(0.3), false},
		{"missing model never falls back", videoprovider.ModelSeedance20, "720p", nil, false},
		{"unsupported tier cannot use default", videoprovider.ModelSeedance25, "1080p", nil, false},
		{"unknown tier", videoprovider.ModelSeedance25, "banana", nil, true},
		{"unknown model", "sora", "720p", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := ResolveSeedanceSalesPrice(context.Background(), svc, seedanceSalesTestGroup(), tc.model, tc.tier)
			if tc.err {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.want, got)
		})
	}
}

func TestProjectSeedanceSalesPricesLeavesLegacyGroupUnchanged(t *testing.T) {
	group := seedanceSalesTestGroup()
	svc := seedanceSalesTestService(&Channel{Status: StatusActive, ModelPricing: []ChannelModelPricing{
		{Platform: PlatformSeedance, Models: []string{videoprovider.ModelSeedance20Fast}, BillingMode: BillingModeVideo, PerRequestPrice: seedanceSalesTestPrice(0.25)},
	}})
	projected, err := ProjectSeedanceSalesPrices(context.Background(), svc, group)
	require.NoError(t, err)
	require.NotSame(t, group, projected)
	require.Equal(t, map[string]map[string]float64{videoprovider.ModelSeedance20Fast: {"480p": 0.25, "720p": 0.25}}, projected.VideoModelPrices)
	require.Equal(t, map[string]map[string]float64{videoprovider.ModelSeedance20: {"720p": 0.1}}, group.VideoModelPrices)
	projected.VideoModelPrices[videoprovider.ModelSeedance20Fast]["720p"] = 8
	require.Equal(t, 0.1, group.VideoModelPrices[videoprovider.ModelSeedance20]["720p"])
}

type seedanceSalesGroupRepo struct {
	GroupRepository
	groups map[int64]*Group
	reads  int
}

func (r *seedanceSalesGroupRepo) GetByIDLite(_ context.Context, id int64) (*Group, error) {
	r.reads++
	if group := r.groups[id]; group != nil {
		return group, nil
	}
	return nil, ErrGroupNotFound
}

func TestSeedanceSalesImportRequiresIdenticalPrices(t *testing.T) {
	for _, tc := range []struct {
		name     string
		second   map[string]map[string]float64
		conflict bool
	}{
		{"canonical aliases agree", map[string]map[string]float64{"bytedance/seedance-2.5": {"720P": 0.2, "480p": 0}}, false},
		{"different price", map[string]map[string]float64{videoprovider.ModelSeedance25: {"720p": 0.3, "480p": 0}}, true},
		{"missing tier", map[string]map[string]float64{videoprovider.ModelSeedance25: {"720p": 0.2}}, true},
		{"unconfigured group", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &seedanceSalesGroupRepo{groups: map[int64]*Group{
				1: {ID: 1, Platform: PlatformSeedance, VideoModelPrices: map[string]map[string]float64{videoprovider.ModelSeedance25: {"720p": 0.2, "480p": 0}}},
				2: {ID: 2, Platform: PlatformSeedance, VideoModelPrices: tc.second},
				3: {ID: 3, Platform: PlatformOpenAI},
			}}
			svc := &ChannelService{groupRepo: repo}
			prices, err := svc.PreviewSeedanceSalesImport(context.Background(), []int64{1, 1, 3, 2})
			require.Equal(t, 3, repo.reads)
			if tc.conflict {
				require.Equal(t, http.StatusConflict, infraerrors.Code(err))
				require.Equal(t, "SEEDANCE_LEGACY_PRICING_CONFLICT", infraerrors.Reason(err))
				require.Nil(t, prices)
				return
			}
			require.NoError(t, err)
			require.Len(t, prices, 1)
			require.Equal(t, []string{videoprovider.ModelSeedance25}, prices[0].Models)
			require.Equal(t, BillingModeVideo, prices[0].BillingMode)
			require.Equal(t, PlatformSeedance, prices[0].Platform)
			require.Equal(t, "480p", prices[0].Intervals[0].TierLabel)
			require.Equal(t, 0.0, *prices[0].Intervals[0].PerRequestPrice)
			require.Equal(t, "720p", prices[0].Intervals[1].TierLabel)
			require.Equal(t, 0.2, *prices[0].Intervals[1].PerRequestPrice)
			require.Contains(t, repo.groups[2].VideoModelPrices, "bytedance/seedance-2.5")
		})
	}
}

type seedanceSalesChannelRepo struct {
	ChannelRepository
	channel *Channel
	loadErr error
}

func (r *seedanceSalesChannelRepo) ExistsByName(context.Context, string) (bool, error) {
	return false, nil
}
func (r *seedanceSalesChannelRepo) Create(_ context.Context, channel *Channel) error {
	channel.ID = 1
	r.channel = channel.Clone()
	return nil
}
func (r *seedanceSalesChannelRepo) GetByID(context.Context, int64) (*Channel, error) {
	return r.channel.Clone(), nil
}
func (r *seedanceSalesChannelRepo) Update(_ context.Context, channel *Channel) error {
	r.channel = channel.Clone()
	return nil
}
func (r *seedanceSalesChannelRepo) ListAll(context.Context) ([]Channel, error) {
	return nil, r.loadErr
}

func TestSeedanceSalesOwnershipPersistsWhenPricesAndFeaturesAreCleared(t *testing.T) {
	repo := &seedanceSalesChannelRepo{}
	svc := NewChannelService(repo, nil, nil, nil, nil)
	created, err := svc.Create(context.Background(), &CreateChannelInput{Name: "seedance", ModelPricing: []ChannelModelPricing{
		{Platform: PlatformSeedance, Models: []string{"bytedance/seedance-2.5"}, BillingMode: BillingModeVideo, PerRequestPrice: seedanceSalesTestPrice(0.2)},
	}})
	require.NoError(t, err)
	require.Equal(t, true, created.FeaturesConfig[seedanceSalesPricingKey])
	require.Equal(t, []string{videoprovider.ModelSeedance25}, created.ModelPricing[0].Models)
	empty := []ChannelModelPricing{}
	updated, err := svc.Update(context.Background(), created.ID, &UpdateChannelInput{ModelPricing: &empty, FeaturesConfig: map[string]any{seedanceSalesPricingKey: false}})
	require.NoError(t, err)
	require.Empty(t, updated.ModelPricing)
	require.Equal(t, true, updated.FeaturesConfig[seedanceSalesPricingKey])
}

func TestSeedanceSalesCacheFailureNeverRevivesLegacyPrices(t *testing.T) {
	svc := NewChannelService(&seedanceSalesChannelRepo{loadErr: errors.New("database unavailable")}, nil, nil, nil, nil)
	for range 2 {
		price, _, err := ResolveSeedanceSalesPrice(context.Background(), svc, seedanceSalesTestGroup(), videoprovider.ModelSeedance20, "720p")
		require.Error(t, err)
		require.Nil(t, price)
	}
}

func TestSeedanceSalesLegacyChannelCanBeEditedWithoutRepricing(t *testing.T) {
	for _, mode := range []BillingMode{BillingModeToken, BillingModePerRequest, BillingModeImage} {
		for _, resend := range []bool{false, true} {
			t.Run(string(mode)+map[bool]string{false: "/omitted", true: "/resubmitted"}[resend], func(t *testing.T) {
				legacy := ChannelModelPricing{ID: 9, ChannelID: 1, CreatedAt: time.Now(),
					Platform: PlatformSeedance, Models: []string{videoprovider.ModelSeedance20},
					BillingMode: mode, InputPrice: seedanceSalesTestPrice(0.000001), PerRequestPrice: seedanceSalesTestPrice(0.2)}
				repo := &seedanceSalesChannelRepo{channel: &Channel{ID: 1, Name: "legacy", Status: StatusActive,
					ModelPricing: []ChannelModelPricing{legacy}, AccountStatsPricingRules: []AccountStatsPricingRule{{
						Pricing: []ChannelModelPricing{{Platform: PlatformSeedance, Models: legacy.Models, BillingMode: BillingModeToken}},
					}}}}
				svc := NewChannelService(repo, nil, nil, nil, nil)
				description := "updated description"
				input := &UpdateChannelInput{Description: &description}
				if resend {
					row := legacy.Clone()
					row.ID, row.ChannelID, row.CreatedAt = 0, 0, time.Time{}
					row.Intervals = []PricingInterval{}
					rows := []ChannelModelPricing{row, {Platform: PlatformOpenAI, Models: []string{"gpt-test"}, BillingMode: BillingModeToken}}
					input.ModelPricing = &rows
				}
				updated, err := svc.Update(context.Background(), 1, input)
				require.NoError(t, err, "unrelated channel edits must not require migrating old Seedance prices")
				require.Equal(t, description, updated.Description)
				require.Equal(t, mode, updated.ModelPricing[0].BillingMode)
				require.False(t, channelOwnsSeedanceSalesPricing(updated))
				price, source, err := ResolveSeedanceSalesPrice(context.Background(), seedanceSalesTestService(updated),
					seedanceSalesTestGroup(), videoprovider.ModelSeedance20, "720p")
				require.NoError(t, err)
				require.Equal(t, PricingSourceGroup, source)
				require.Equal(t, 0.1, *price)

				changed := []ChannelModelPricing{legacy.Clone()}
				changed[0].PerRequestPrice = seedanceSalesTestPrice(0.3)
				_, err = svc.Update(context.Background(), 1, &UpdateChannelInput{ModelPricing: &changed})
				require.Equal(t, "INVALID_SEEDANCE_SALES_PRICE", infraerrors.Reason(err), "edited Seedance prices must use USD/second")
			})
		}
	}
}

func TestSeedanceSalesPricingValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		price ChannelModelPricing
	}{
		{"per task is not a sales unit", ChannelModelPricing{BillingMode: BillingModePerRequest, PerRequestPrice: seedanceSalesTestPrice(1)}},
		{"unsupported resolution", ChannelModelPricing{BillingMode: BillingModeVideo, Intervals: []PricingInterval{{TierLabel: "1080p", PerRequestPrice: seedanceSalesTestPrice(1)}}}},
		{"negative sales price", ChannelModelPricing{BillingMode: BillingModeVideo, PerRequestPrice: seedanceSalesTestPrice(-1)}},
		{"time pricing cannot be ignored", ChannelModelPricing{BillingMode: BillingModeVideo, PerRequestPrice: seedanceSalesTestPrice(1), TimePricing: &ChannelTimePricing{Periods: []ChannelTimePricingPeriod{{StartTime: "09:00", EndTime: "18:00", Multiplier: 2}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.price.Platform = PlatformSeedance
			tc.price.Models = []string{videoprovider.ModelSeedance25}
			err := normalizeSeedanceSalesPricing([]ChannelModelPricing{tc.price})
			require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
			require.Equal(t, "INVALID_SEEDANCE_SALES_PRICE", infraerrors.Reason(err))
			require.NotContains(t, infraerrors.Message(err), "account cost")
		})
	}
}
