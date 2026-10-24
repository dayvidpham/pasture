package sessionapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dayvidpham/pasture/internal/peasantproject"
	"github.com/dayvidpham/pasture/internal/sessionapi"
)

func serveSettings(t *testing.T, source peasantproject.Source) (string, *sessionapi.Service, func()) {
	t.Helper()
	f := seedSessions(t)
	service := f.service()
	service.ProjectSource = source
	server := httptest.NewServer(sessionapi.NewHandler(service))
	return server.URL, service, server.Close
}

func postPeasantSettings(t *testing.T, base, body string) (*http.Response, sessionapi.PeasantIntegration, map[string]any) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, base+"/api/v1/settings/peasant", strings.NewReader(body))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	raw, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	response.Body.Close()
	if response.StatusCode == http.StatusOK {
		var integration sessionapi.PeasantIntegration
		require.NoError(t, json.Unmarshal(raw, &integration), "body: %s", raw)
		return response, integration, nil
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(raw, &envelope), "body: %s", raw)
	return response, sessionapi.PeasantIntegration{}, map[string]any{
		"code":    envelope.Error.Code,
		"message": envelope.Error.Message,
	}
}

func TestPeasantSettingsTogglesTheRuntimeSource(t *testing.T) {
	t.Parallel()

	f := seedSessions(t)
	service := f.service()
	switchSource := peasantproject.NewSwitch(func(context.Context) (peasantproject.Source, error) {
		return &fakePeasantSource{
			projects: map[peasantproject.Key]peasantproject.Project{
				{Harness: "claude-code", Session: "active"}: {Name: "org/repo", Hash: strings.Repeat("a", 64)},
			},
			status: peasantproject.Status{Enabled: true, Source: peasantproject.SourceDB, State: peasantproject.StateOK},
		}, nil
	}, nil)
	service.ProjectSource = switchSource
	server := httptest.NewServer(sessionapi.NewHandler(service))
	t.Cleanup(server.Close)

	page, err := service.List(context.Background(), sessionapi.ListOptions{Limit: 10})
	require.NoError(t, err)
	for _, item := range page.Items {
		require.Empty(t, item.Project, "off means no project fields")
	}
	inspect, err := service.Inspect(context.Background())
	require.NoError(t, err)
	require.Equal(t, sessionapi.PeasantIntegrationDisabled, inspect.PeasantIntegration.Status)

	response, integration, _ := postPeasantSettings(t, server.URL, `{"enabled":true}`)
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.True(t, integration.Enabled)
	require.Equal(t, peasantproject.SourceDB, integration.Source)
	require.Equal(t, sessionapi.PeasantIntegrationOK, integration.Status)

	inspect, err = service.Inspect(context.Background())
	require.NoError(t, err)
	require.Equal(t, sessionapi.PeasantIntegrationOK, inspect.PeasantIntegration.Status)
	require.True(t, inspect.PeasantIntegration.Enabled)

	response, integration, _ = postPeasantSettings(t, server.URL, `{"enabled":false}`)
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.False(t, integration.Enabled)
	require.Equal(t, peasantproject.SourceDisabled, integration.Source)
	require.Equal(t, sessionapi.PeasantIntegrationDisabled, integration.Status)
	require.Contains(t, integration.Message, "turn it on from the web console")

	inspect, err = service.Inspect(context.Background())
	require.NoError(t, err)
	require.Equal(t, sessionapi.PeasantIntegrationDisabled, inspect.PeasantIntegration.Status,
		"a switch that is off is disabled, never unavailable")
	page, err = service.List(context.Background(), sessionapi.ListOptions{Limit: 10})
	require.NoError(t, err)
	for _, item := range page.Items {
		require.Empty(t, item.Project, "turning off must clear project fields on the next read")
	}
}

func TestPeasantSettingsRejectsMalformedBodies(t *testing.T) {
	t.Parallel()

	switchSource := peasantproject.NewSwitch(func(context.Context) (peasantproject.Source, error) {
		return &fakePeasantSource{status: peasantproject.Status{Enabled: true, Source: peasantproject.SourceDB, State: peasantproject.StateOK}}, nil
	}, nil)
	t.Cleanup(func() { _ = switchSource.Close() })
	base, _, closeServer := serveSettings(t, switchSource)
	defer closeServer()

	for _, body := range []string{`{`, `{}`, `{"enabled":"yes"}`, `{"enabled":true,"extra":1}`} {
		response, _, envelope := postPeasantSettings(t, base, body)
		require.Equal(t, http.StatusBadRequest, response.StatusCode, "body %s", body)
		require.Equal(t, "invalid_body", envelope["code"], "body %s", body)
		require.NotEmpty(t, envelope["message"], "body %s", body)
	}
	require.False(t, switchSource.Status().Enabled, "a malformed request must not change the switch")
}

func TestPeasantSettingsReports409WhenTheSourceCannotOpen(t *testing.T) {
	t.Parallel()

	switchSource := peasantproject.NewSwitch(func(context.Context) (peasantproject.Source, error) {
		return nil, errors.New("no Peasant database was found at /tmp/peasant.db; start Peasant or pass --peasant-db/--peasant-url — Peasant project enrichment stays off")
	}, nil)
	t.Cleanup(func() { _ = switchSource.Close() })
	base, service, closeServer := serveSettings(t, switchSource)
	defer closeServer()

	response, _, envelope := postPeasantSettings(t, base, `{"enabled":true}`)
	require.Equal(t, http.StatusConflict, response.StatusCode)
	require.Equal(t, "peasant_settings_refused", envelope["code"])
	require.Contains(t, envelope["message"], "no Peasant database was found")
	require.Contains(t, envelope["message"], "--peasant-db")
	require.False(t, switchSource.Status().Enabled)

	inspect, err := service.Inspect(context.Background())
	require.NoError(t, err)
	require.Equal(t, sessionapi.PeasantIntegrationDisabled, inspect.PeasantIntegration.Status)
	// The session API keeps working while enrichment is off.
	page, err := service.List(context.Background(), sessionapi.ListOptions{Limit: 10})
	require.NoError(t, err)
	require.NotEmpty(t, page.Items)
}

func TestPeasantSettingsUnavailableWithoutASwitch(t *testing.T) {
	t.Parallel()

	base, _, closeServer := serveSettings(t, &fakePeasantSource{
		status: peasantproject.Status{Enabled: true, Source: peasantproject.SourceDB, State: peasantproject.StateOK},
	})
	defer closeServer()

	response, _, envelope := postPeasantSettings(t, base, `{"enabled":true}`)
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	require.Equal(t, "peasant_settings_unavailable", envelope["code"])
	require.Contains(t, envelope["message"], "no configurable Peasant project source")
}

func TestPeasantSettingsRefusesNonLoopbackHostAndOrigin(t *testing.T) {
	t.Parallel()

	switchSource := peasantproject.NewSwitch(func(context.Context) (peasantproject.Source, error) {
		return &fakePeasantSource{status: peasantproject.Status{Enabled: true, Source: peasantproject.SourceDB, State: peasantproject.StateOK}}, nil
	}, nil)
	t.Cleanup(func() { _ = switchSource.Close() })
	base, _, closeServer := serveSettings(t, switchSource)
	defer closeServer()

	request, err := http.NewRequest(http.MethodPost, base+"/api/v1/settings/peasant", bytes.NewBufferString(`{"enabled":true}`))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	request.Host = "evil.example"
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, http.StatusForbidden, response.StatusCode)
	require.False(t, switchSource.Status().Enabled)

	request, err = http.NewRequest(http.MethodPost, base+"/api/v1/settings/peasant", bytes.NewBufferString(`{"enabled":true}`))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://evil.example")
	response, err = http.DefaultClient.Do(request)
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, http.StatusForbidden, response.StatusCode)
	require.False(t, switchSource.Status().Enabled)
}
