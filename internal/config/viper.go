package config

import (
	"fmt"
	"os"

	"github.com/dayvidpham/pasture/internal/types"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// bindChangedFlag sets v[viperKey] to the flag's value only when the flag was
// explicitly provided by the user (flag.Changed == true). This preserves the
// correct Viper priority chain (CLI > env > YAML > default) because Viper's
// BindPFlag alone cannot distinguish a flag that was set by the user from one
// that still holds its registered default value.
//
// The lookup checks both cmd.Flags() (local flags) and cmd.PersistentFlags()
// because cobra stores them in separate flag sets and Flags().Lookup() does
// NOT find persistent flags.
func bindChangedFlag(v *viper.Viper, viperKey string, cmd *cobra.Command, flagName string) {
	// Check local flags first, then persistent flags.
	f := cmd.Flags().Lookup(flagName)
	if f == nil {
		f = cmd.PersistentFlags().Lookup(flagName)
	}
	if f == nil {
		// Flag not registered on this command — skip silently.
		return
	}
	if f.Changed {
		v.Set(viperKey, f.Value.String())
	}
}

// bindEnvVar sets v[viperKey] from environment variable envVar when it is
// present AND non-empty, reading through the injected lookupEnv (os.LookupEnv in
// production, a fixture map in tests). A set-but-empty env var (e.g.
// PASTURE_AUDIT_TRAIL="") is treated as UNSET so it does not mask the default —
// this matches viper.BindEnv's default (allowEmptyEnv=false) that this seam
// replaced; without the emptiness guard an exported-but-blank variable would
// land in the override tier and resolve to "" (for audit_trail, "" maps to the
// non-durable in-memory backend).
//
// Like bindChangedFlag it writes via v.Set — Viper's override tier — so callers
// MUST invoke bindEnvVar BEFORE bindChangedFlag: a changed flag's later Set then
// overwrites the env value for that key, giving CLI > env, while both still
// outrank the config-file and default tiers (CLI > env > YAML > default).
//
// Injecting lookupEnv is what keeps env resolution off the process-global
// environment, so config resolution tests can run in parallel without racing on
// os.Setenv.
func bindEnvVar(v *viper.Viper, viperKey, envVar string, lookupEnv func(string) (string, bool)) {
	if val, ok := lookupEnv(envVar); ok && val != "" {
		v.Set(viperKey, val)
	}
}

// ResolvePasturedConfig resolves the full PasturedConfig, including audit-trail
// settings, using the default config file path.
//
// Config-file read errors are silently ignored (missing default config file is
// not fatal — defaults and environment variables still apply). Use
// ResolvePasturedConfigFromFile for explicit paths where an error should be
// surfaced to the caller.
func ResolvePasturedConfig(cmd *cobra.Command) PasturedConfig {
	cfg, _ := resolvePasturedConfigWithFile(cmd, DefaultConfigPath(), os.LookupEnv)
	return cfg
}

// ResolvePasturedConfigFromFile resolves the full PasturedConfig using an
// explicitly provided config file path (e.g., from --config CLI flag).
//
// The returned error indicates that the config file could not be read (missing
// or malformed). The config is still populated with defaults, environment
// variables, and CLI flags — callers should decide whether to fail-fast or
// continue with the partial config.
func ResolvePasturedConfigFromFile(cmd *cobra.Command, configFile string) (PasturedConfig, error) {
	return resolvePasturedConfigWithFile(cmd, configFile, os.LookupEnv)
}

// resolvePasturedConfigWithFile resolves PasturedConfig from the given file,
// reading environment variables through the injected lookupEnv. Production
// callers pass os.LookupEnv; tests pass a fixture map so resolution can be
// exercised in parallel without touching the process environment.
func resolvePasturedConfigWithFile(
	cmd *cobra.Command,
	configFile string,
	lookupEnv func(string) (string, bool),
) (PasturedConfig, error) {
	v := viper.New()

	// --- Defaults ---
	v.SetDefault("audit_trail", string(types.BackendSqlite))
	v.SetDefault("audit_db_path", "")

	// --- Config file ---
	var fileErr error
	if configFile != "" {
		v.SetConfigFile(configFile)
		v.SetConfigType("yaml")
		if err := v.ReadInConfig(); err != nil {
			fileErr = fmt.Errorf(
				"config: cannot read config file %q"+
					" — ensure the file exists and is valid YAML"+
					" (set via --config flag or the default ~/.config/pasture/config.yaml)"+
					": %w",
				configFile, err,
			)
		}
	}

	// --- Environment variables, then CLI flags ---
	// For each key, bind the env var then the changed flag, in that order:
	// both write into Viper's override tier, so applying env first and the
	// changed flag last makes the flag win (CLI > env), while the override tier
	// still outranks the file and default tiers (CLI > env > YAML > default).
	// Keeping the two binds adjacent per key — rather than two separate loops —
	// makes that ordering structural, and a new config key just adds a row.
	for _, b := range []struct{ viperKey, envVar, flagName string }{
		{"audit_trail", EnvAuditTrail, "audit-trail"},
		{"audit_db_path", EnvAuditDBPath, "audit-db-path"},
	} {
		bindEnvVar(v, b.viperKey, b.envVar, lookupEnv)
		bindChangedFlag(v, b.viperKey, cmd, b.flagName)
	}

	cfg := PasturedConfig{
		AuditTrail:  types.AuditTrailBackend(v.GetString("audit_trail")),
		AuditDBPath: v.GetString("audit_db_path"),
	}
	return cfg, fileErr
}
