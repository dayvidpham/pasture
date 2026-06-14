package config

import (
	"fmt"

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

// ResolvePasturedConfig resolves the full PasturedConfig, including audit-trail
// settings, using the default config file path.
//
// Config-file read errors are silently ignored (missing default config file is
// not fatal — defaults and environment variables still apply). Use
// ResolvePasturedConfigFromFile for explicit paths where an error should be
// surfaced to the caller.
func ResolvePasturedConfig(cmd *cobra.Command) PasturedConfig {
	cfg, _ := resolvePasturedConfigWithFile(cmd, DefaultConfigPath())
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
	return resolvePasturedConfigWithFile(cmd, configFile)
}

// resolvePasturedConfigWithFile resolves PasturedConfig from the given file.
func resolvePasturedConfigWithFile(cmd *cobra.Command, configFile string) (PasturedConfig, error) {
	v := viper.New()

	// --- Defaults ---
	v.SetDefault("audit_trail", string(types.BackendSqlite))
	v.SetDefault("audit_db_path", "")

	// --- Environment variables ---
	v.BindEnv("audit_trail", EnvAuditTrail)    //nolint:errcheck
	v.BindEnv("audit_db_path", EnvAuditDBPath) //nolint:errcheck

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

	// --- CLI flags ---
	bindChangedFlag(v, "audit_trail", cmd, "audit-trail")
	bindChangedFlag(v, "audit_db_path", cmd, "audit-db-path")

	cfg := PasturedConfig{
		AuditTrail:  types.AuditTrailBackend(v.GetString("audit_trail")),
		AuditDBPath: v.GetString("audit_db_path"),
	}
	return cfg, fileErr
}
