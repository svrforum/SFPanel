package appstore

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractForkMeta_EnvFromMapForm(t *testing.T) {
	composeYAML := `services:
  web:
    image: nginx:1.25
    environment:
      LOG_LEVEL: info
      TZ: Asia/Seoul
`
	envValues := map[string]string{
		"LOG_LEVEL": "debug",
		"TZ":        "UTC",
	}
	user := UserForkInput{Name: "My Stack", Description: "test", Category: "테스트"}
	meta, compose := ExtractForkMeta("my-stack", composeYAML, envValues, user)

	require.NotEmpty(t, meta.ID)
	require.Contains(t, meta.ID, "fork-")
	require.Equal(t, "My Stack", meta.Name)
	require.Equal(t, "테스트", meta.Category)
	require.Equal(t, "1.0.0", meta.Version)
	require.Len(t, meta.Env, 2)
	envByKey := map[string]AppStoreEnvDef{}
	for _, e := range meta.Env {
		envByKey[e.Key] = e
	}
	// Env values come from envValues (current runtime values), not the YAML defaults.
	require.Equal(t, "debug", envByKey["LOG_LEVEL"].Default)
	require.Equal(t, "UTC", envByKey["TZ"].Default)
	require.Equal(t, composeYAML, compose)
}

func TestExtractForkMeta_EnvFromListForm(t *testing.T) {
	composeYAML := `services:
  app:
    image: app:1
    environment:
      - LOG_LEVEL=info
      - DEBUG=false
`
	envValues := map[string]string{
		"LOG_LEVEL": "info",
		"DEBUG":     "false",
	}
	meta, _ := ExtractForkMeta("app", composeYAML, envValues, UserForkInput{Name: "App"})
	require.Len(t, meta.Env, 2)
}

func TestExtractForkMeta_DefaultCategory(t *testing.T) {
	meta, _ := ExtractForkMeta("x", "services: {}", nil, UserForkInput{Name: "X"})
	require.Equal(t, "내 Templates", meta.Category)
}

func TestExtractForkMeta_NoEnvSection(t *testing.T) {
	composeYAML := `services:
  web:
    image: nginx
`
	meta, _ := ExtractForkMeta("y", composeYAML, nil, UserForkInput{Name: "Y"})
	require.Empty(t, meta.Env)
}

func TestExtractForkMeta_IDStableShape(t *testing.T) {
	meta, _ := ExtractForkMeta("z", "services: {}", nil, UserForkInput{Name: "Z"})
	require.Regexp(t, `^fork-[a-f0-9]{8}$`, meta.ID)
}
