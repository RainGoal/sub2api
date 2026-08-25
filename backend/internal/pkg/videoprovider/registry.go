package videoprovider

import (
	"fmt"
	"sort"
	"strings"
)

type Registration struct {
	Driver  Driver
	Aliases []string
}

type Descriptor struct {
	ID             ID            `json:"id"`
	DisplayName    string        `json:"display_name"`
	DefaultBaseURL string        `json:"default_base_url"`
	ModelIDs       []string      `json:"model_ids"`
	BillingPolicy  BillingPolicy `json:"billing_policy"`
}

type Registry struct {
	drivers map[ID]Driver
	aliases map[string]ID
}

func NewRegistry(registrations ...Registration) (*Registry, error) {
	registry := &Registry{
		drivers: make(map[ID]Driver, len(registrations)),
		aliases: make(map[string]ID, len(registrations)*2),
	}
	for _, registration := range registrations {
		driver := registration.Driver
		if driver == nil || strings.TrimSpace(string(driver.ID())) == "" {
			return nil, fmt.Errorf("video provider registration requires a driver id")
		}
		id := ID(strings.ToLower(strings.TrimSpace(string(driver.ID()))))
		if _, exists := registry.drivers[id]; exists {
			return nil, fmt.Errorf("video provider %q is already registered", id)
		}
		if _, err := buildProviderURL(driver.DefaultBaseURL(), ""); err != nil {
			return nil, fmt.Errorf("video provider %q has an invalid default base URL: %w", id, err)
		}
		registry.drivers[id] = driver
		for _, alias := range append([]string{string(id)}, registration.Aliases...) {
			normalized := strings.ToLower(strings.TrimSpace(alias))
			if normalized == "" {
				continue
			}
			if existing, exists := registry.aliases[normalized]; exists && existing != id {
				return nil, fmt.Errorf("video provider alias %q is already registered", normalized)
			}
			registry.aliases[normalized] = id
		}
	}
	return registry, nil
}

func MustNewRegistry(registrations ...Registration) *Registry {
	registry, err := NewRegistry(registrations...)
	if err != nil {
		panic(err)
	}
	return registry
}

func (r *Registry) NormalizeID(value string, defaultID ID) (ID, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		value = string(defaultID)
	}
	if r == nil {
		return "", fmt.Errorf("video provider registry is unavailable")
	}
	id, ok := r.aliases[value]
	if !ok {
		return "", fmt.Errorf("unsupported video provider %q", value)
	}
	return id, nil
}

func (r *Registry) Resolve(value string, defaultID ID) (Driver, error) {
	id, err := r.NormalizeID(value, defaultID)
	if err != nil {
		return nil, err
	}
	driver := r.drivers[id]
	if driver == nil {
		return nil, fmt.Errorf("video provider %q is not registered", id)
	}
	return driver, nil
}

func (r *Registry) ProviderIDs() []ID {
	if r == nil {
		return nil
	}
	ids := make([]ID, 0, len(r.drivers))
	for id := range r.drivers {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (r *Registry) Descriptors() []Descriptor {
	ids := r.ProviderIDs()
	descriptors := make([]Descriptor, 0, len(ids))
	for _, id := range ids {
		driver := r.drivers[id]
		descriptors = append(descriptors, Descriptor{
			ID: id, DisplayName: driver.DisplayName(), DefaultBaseURL: driver.DefaultBaseURL(),
			ModelIDs: append([]string(nil), driver.ModelIDs()...), BillingPolicy: driver.BillingPolicy(),
		})
	}
	return descriptors
}

var defaultRegistry = MustNewRegistry(
	Registration{Driver: bblabuDriver{}, Aliases: []string{"bblabu"}},
	Registration{Driver: fflinkDriver{}, Aliases: []string{"fflink"}},
)

func NormalizeID(value string) (ID, error) {
	return defaultRegistry.NormalizeID(value, DefaultProviderID)
}

func Resolve(value string) (Driver, error) {
	return defaultRegistry.Resolve(value, DefaultProviderID)
}

func ProviderIDs() []ID {
	return defaultRegistry.ProviderIDs()
}

func Descriptors() []Descriptor {
	return defaultRegistry.Descriptors()
}

func DefaultModelIDs() []string {
	seen := make(map[string]struct{})
	models := make([]string, 0)
	for _, descriptor := range Descriptors() {
		for _, model := range descriptor.ModelIDs {
			dedupeKey := strings.ToLower(strings.TrimSpace(model))
			if canonical, ok := CanonicalModel(model); ok {
				dedupeKey = canonical
			}
			if _, exists := seen[dedupeKey]; exists {
				continue
			}
			seen[dedupeKey] = struct{}{}
			models = append(models, model)
		}
	}
	sort.Strings(models)
	return models
}
