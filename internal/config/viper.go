package config

import (
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

// resolveConnectionConfigWithViper builds a Viper instance wired to the
// provided cobra.Command flags and optional config-file path, then returns
// a fully-resolved ConnectionConfig.
//
// Priority (highest → lowest):
//  1. CLI flags (only when the flag was explicitly set by the user)
//  2. Environment variables (TEMPORAL_NAMESPACE, TEMPORAL_TASK_QUEUE, TEMPORAL_ADDRESS)
//  3. YAML config file at configFile (skipped when empty)
//  4. Built-in defaults
func resolveConnectionConfigWithViper(cmd *cobra.Command, configFile string) ConnectionConfig {
	v := viper.New()

	// --- Defaults ---
	v.SetDefault("connection.namespace", "default")
	v.SetDefault("connection.task_queue", "pasture")
	v.SetDefault("connection.server_address", "localhost:7233")

	// --- Environment variables ---
	v.BindEnv("connection.namespace", EnvNamespace)    //nolint:errcheck
	v.BindEnv("connection.task_queue", EnvTaskQueue)   //nolint:errcheck
	v.BindEnv("connection.server_address", EnvAddress) //nolint:errcheck

	// --- Config file (optional) ---
	if configFile != "" {
		v.SetConfigFile(configFile)
		v.SetConfigType("yaml")
		v.ReadInConfig() //nolint:errcheck — missing file is not fatal
	}

	// --- CLI flags (highest priority) ---
	// v.Set() overrides everything including env vars and YAML values.
	bindChangedFlag(v, "connection.namespace", cmd, "namespace")
	bindChangedFlag(v, "connection.task_queue", cmd, "task-queue")
	bindChangedFlag(v, "connection.server_address", cmd, "address")

	return ConnectionConfig{
		Namespace:     v.GetString("connection.namespace"),
		TaskQueue:     v.GetString("connection.task_queue"),
		ServerAddress: v.GetString("connection.server_address"),
	}
}

// ResolveConnectionConfig resolves a ConnectionConfig using the default config
// file path (~/.config/pasture/config.yaml).
func ResolveConnectionConfig(cmd *cobra.Command) ConnectionConfig {
	return resolveConnectionConfigWithViper(cmd, DefaultConfigPath())
}

// ResolveConnectionConfigFromFile resolves a ConnectionConfig using the
// explicitly provided config file path. This variant exists primarily for
// testing with a temporary YAML file.
func ResolveConnectionConfigFromFile(cmd *cobra.Command, configFile string) ConnectionConfig {
	return resolveConnectionConfigWithViper(cmd, configFile)
}

// ResolvePasturedConfig resolves the full PasturedConfig, including audit-trail
// settings, using the default config file path.
func ResolvePasturedConfig(cmd *cobra.Command) PasturedConfig {
	return resolvePasturedConfigWithFile(cmd, DefaultConfigPath())
}

// resolvePasturedConfigWithFile resolves PasturedConfig from the given file.
func resolvePasturedConfigWithFile(cmd *cobra.Command, configFile string) PasturedConfig {
	v := viper.New()

	// --- Defaults ---
	v.SetDefault("connection.namespace", "default")
	v.SetDefault("connection.task_queue", "pasture")
	v.SetDefault("connection.server_address", "localhost:7233")
	v.SetDefault("audit_trail", string(types.BackendSqlite))
	v.SetDefault("audit_db_path", "")

	// --- Environment variables ---
	v.BindEnv("connection.namespace", EnvNamespace)    //nolint:errcheck
	v.BindEnv("connection.task_queue", EnvTaskQueue)   //nolint:errcheck
	v.BindEnv("connection.server_address", EnvAddress) //nolint:errcheck
	v.BindEnv("audit_trail", EnvAuditTrail)            //nolint:errcheck
	v.BindEnv("audit_db_path", EnvAuditDBPath)         //nolint:errcheck

	// --- Config file ---
	if configFile != "" {
		v.SetConfigFile(configFile)
		v.SetConfigType("yaml")
		v.ReadInConfig() //nolint:errcheck
	}

	// --- CLI flags ---
	bindChangedFlag(v, "connection.namespace", cmd, "namespace")
	bindChangedFlag(v, "connection.task_queue", cmd, "task-queue")
	bindChangedFlag(v, "connection.server_address", cmd, "address")
	bindChangedFlag(v, "audit_trail", cmd, "audit-trail")
	bindChangedFlag(v, "audit_db_path", cmd, "audit-db-path")

	return PasturedConfig{
		Connection: ConnectionConfig{
			Namespace:     v.GetString("connection.namespace"),
			TaskQueue:     v.GetString("connection.task_queue"),
			ServerAddress: v.GetString("connection.server_address"),
		},
		AuditTrail:  types.AuditTrailBackend(v.GetString("audit_trail")),
		AuditDBPath: v.GetString("audit_db_path"),
	}
}

// ResolvePastureMsgConfig resolves the full PastureMsgConfig using the default
// config file path.
func ResolvePastureMsgConfig(cmd *cobra.Command) PastureMsgConfig {
	return resolvePastureMsgConfigWithFile(cmd, DefaultConfigPath())
}

// resolvePastureMsgConfigWithFile resolves PastureMsgConfig from the given file.
func resolvePastureMsgConfigWithFile(cmd *cobra.Command, configFile string) PastureMsgConfig {
	v := viper.New()

	// --- Defaults ---
	v.SetDefault("connection.namespace", "default")
	v.SetDefault("connection.task_queue", "pasture")
	v.SetDefault("connection.server_address", "localhost:7233")
	v.SetDefault("default_format", string(types.OutputText))

	// --- Environment variables ---
	v.BindEnv("connection.namespace", EnvNamespace)    //nolint:errcheck
	v.BindEnv("connection.task_queue", EnvTaskQueue)   //nolint:errcheck
	v.BindEnv("connection.server_address", EnvAddress) //nolint:errcheck

	// --- Config file ---
	if configFile != "" {
		v.SetConfigFile(configFile)
		v.SetConfigType("yaml")
		v.ReadInConfig() //nolint:errcheck
	}

	// --- CLI flags ---
	bindChangedFlag(v, "connection.namespace", cmd, "namespace")
	bindChangedFlag(v, "connection.task_queue", cmd, "task-queue")
	bindChangedFlag(v, "connection.server_address", cmd, "address")
	bindChangedFlag(v, "default_format", cmd, "format")

	return PastureMsgConfig{
		Connection: ConnectionConfig{
			Namespace:     v.GetString("connection.namespace"),
			TaskQueue:     v.GetString("connection.task_queue"),
			ServerAddress: v.GetString("connection.server_address"),
		},
		DefaultFormat: types.OutputFormat(v.GetString("default_format")),
	}
}
