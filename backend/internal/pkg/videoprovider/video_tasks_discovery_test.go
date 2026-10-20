package videoprovider

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVideoTasksDiscoveryRequiresValidModelList(t *testing.T) {
	driver, err := Resolve(string(ProviderVideoTasksJSONV1))
	require.NoError(t, err)
	for name, body := range map[string]string{
		"empty body": "", "null": `null`, "missing data": `{}`,
		"error":           `{"error":{"message":"unavailable"}}`,
		"error with data": `{"error":{"message":"unavailable"},"data":[]}`,
		"null data":       `{"data":null}`, "object data": `{"data":{}}`,
		"string data": `{"data":"unavailable"}`, "top-level array": `[]`,
		"missing id": `{"data":[{}]}`, "null item": `{"data":[null]}`,
		"string item": `{"data":["model"]}`, "empty id": `{"data":[{"id":""}]}`,
		"blank id": `{"data":[{"id":"  "}]}`, "null id": `{"data":[{"id":null}]}`,
		"numeric id":        `{"data":[{"id":42}]}`,
		"newline id":        `{"data":[{"id":"new\nmodel"}]}`,
		"leading control":   `{"data":[{"id":"\nmodel"}]}`,
		"tab id":            `{"data":[{"id":"new\tmodel"}]}`,
		"delete control":    `{"data":[{"id":"new\u007fmodel"}]}`,
		"C1 control":        `{"data":[{"id":"new\u0085model"}]}`,
		"overlong id":       `{"data":[{"id":"` + strings.Repeat("a", 129) + `"}]}`,
		"overlong UTF8 id":  `{"data":[{"id":"` + strings.Repeat("模", 43) + `"}]}`,
		"partially invalid": `{"data":[{"id":"Brand/NewVideo"},{}]}`,
		"trailing JSON":     `{"data":[]} {}`, "malformed": `{"data":[`,
	} {
		t.Run(name, func(t *testing.T) {
			models, err := driver.ParseModels([]byte(body))
			require.Error(t, err)
			require.Nil(t, models, "invalid lists must not be mistaken for an empty catalog")
		})
	}
	models, err := driver.ParseModels([]byte(`{"object":"list","data":[]}`))
	require.NoError(t, err)
	require.NotNil(t, models, "empty discovery must serialize as [] rather than null")
	require.Empty(t, models)
	models, err = driver.ParseModels([]byte(`{"data":[{"id":" Brand/NewVideo "},{"id":"brand/newvideo"},{"id":"Another/NewVideo"},{"id":"Brand/NewVideo"}]}`))
	require.NoError(t, err)
	require.Equal(t, []string{"Another/NewVideo", "Brand/NewVideo", "brand/newvideo"}, models)
	require.False(t, driver.SupportsModel("Brand/NewVideo"), "discovery must not add a model capability")
	maxLengthID := strings.Repeat("a", 128)
	models, err = driver.ParseModels([]byte(`{"data":[{"id":"` + maxLengthID + `"}]}`))
	require.NoError(t, err)
	require.Equal(t, []string{maxLengthID}, models)
}

func TestLegacyVideoDiscoveryPreservesFilteringAndPermissiveParsing(t *testing.T) {
	for _, id := range []ID{ProviderBBLabuV1, ProviderFFLinkV1} {
		t.Run(string(id), func(t *testing.T) {
			driver, err := Resolve(string(id))
			require.NoError(t, err)
			models, err := driver.ParseModels([]byte(`{"data":[{"id":"Brand/NewVideo"},{"id":""},{"id":"Seedance-2.0"},{"id":"seedance-2.0"}]}`))
			require.NoError(t, err)
			require.Equal(t, []string{"Seedance-2.0"}, models)
			for _, body := range []string{`{}`, `null`, `{"data":null}`, `{"data":[]}`} {
				models, err := driver.ParseModels([]byte(body))
				require.NoError(t, err)
				require.Empty(t, models)
			}
		})
	}
}
