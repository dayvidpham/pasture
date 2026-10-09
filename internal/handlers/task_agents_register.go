// Package handlers — task_agents_register.go
//
// Handler for `pasture task agents register` (PROPOSAL-2 §7.4 / §7.9).
//
// Surface:
//
//	pasture task agents register human    --name NAME [--contact CONTACT]
//	pasture task agents register software --name NAME --version VERSION [--source SOURCE]
//	pasture task agents register ml       --role ROLE --provider PROVIDER --model MODEL
//
// Registration is the ONLY way a fresh user can obtain an author identity for
// `pasture task comment add`; that command deliberately never creates identities
// implicitly. The created agent's wire-format ID is printed (via the same
// formatter as `pasture task agents show`) so it can be copied straight into
// `--author`.
//
// The namespace comes from the global --namespace flag and is resolved exactly
// like task creation (internal/tasks.ResolveNamespace), so an agent and the
// tasks it authors share one project namespace by default.
package handlers

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/dayvidpham/provenance"

	pasterrors "github.com/dayvidpham/pasture/internal/errors"
	"github.com/dayvidpham/pasture/internal/formatters"
	"github.com/dayvidpham/pasture/internal/tasks"
	"github.com/dayvidpham/pasture/internal/types"
	"github.com/dayvidpham/pasture/pkg/protocol"
)

// AgentRegisterKind names the PROV-O agent kind a registration targets.
type AgentRegisterKind string

const (
	// AgentRegisterHuman registers a HumanAgent (name + optional contact).
	AgentRegisterHuman AgentRegisterKind = "human"
	// AgentRegisterSoftware registers a SoftwareAgent (name + version + optional source).
	AgentRegisterSoftware AgentRegisterKind = "software"
	// AgentRegisterML registers an MLAgent (role + provider + model).
	AgentRegisterML AgentRegisterKind = "ml"
)

// TaskAgentRegisterInput captures the inputs for `pasture task agents register`.
// Only the fields relevant to Kind are read; the rest stay empty.
type TaskAgentRegisterInput struct {
	DBPath    string
	Namespace string // explicit override; "" → ResolveNamespace default
	Kind      AgentRegisterKind

	// Human
	Name    string
	Contact string

	// Software
	Version string
	Source  string

	// ML
	Role     string
	Provider string
	Model    string
}

// TaskAgentsRegister registers a new agent of the requested kind and prints its
// wire-format ID. Returns the standard (exitCode, error) tuple.
func TaskAgentsRegister(w io.Writer, in TaskAgentRegisterInput, format types.OutputFormat) (int, error) {
	switch in.Kind {
	case AgentRegisterHuman:
		return registerHumanAgent(w, in, format)
	case AgentRegisterSoftware:
		return registerSoftwareAgent(w, in, format)
	case AgentRegisterML:
		return registerMLAgent(w, in, format)
	default:
		return agentRegisterValidation(
			fmt.Sprintf("Unknown agent kind %q.", in.Kind),
			"The kind tells us whether to create a human, software, or machine-learning author identity, and this value isn't one of them.",
			"1. Pass one of the supported kinds:\n"+
				"     pasture task agents register human    --name <name>\n"+
				"     pasture task agents register software --name <name> --version <version>\n"+
				"     pasture task agents register ml       --role <role> --provider <provider> --model <model>",
			nil,
		)
	}
}

func registerHumanAgent(w io.Writer, in TaskAgentRegisterInput, format types.OutputFormat) (int, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return agentRegisterValidation(
			"A name is required to register a human agent.",
			"The --name flag was missing or empty, and a human identity needs a display name.",
			"1. Pass a name with --name:\n"+
				"     pasture task agents register human --name \"Ada Lovelace\"\n"+
				"2. Optionally add contact details:\n"+
				"     pasture task agents register human --name \"Ada Lovelace\" --contact ada@example.com",
			nil,
		)
	}
	ns, code, err := resolveAgentNamespace(in.Namespace)
	if err != nil {
		return code, err
	}
	tr, err := tasks.OpenTaskTracker(in.DBPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tr.Close()

	agent, err := tr.RegisterHumanAgent(ns, name, strings.TrimSpace(in.Contact))
	if err != nil {
		return agentRegisterStoreError("human", err)
	}
	return printRegisteredAgent(w, formatters.AgentEntry{
		AgentId:       agent.ID.String(),
		Kind:          agent.Kind.String(),
		Name:          agent.Name,
		AutomatonRole: protocol.AutomatonRoleNone,
		PastureRole:   protocol.PastureRoleNone,
	}, format)
}

func registerSoftwareAgent(w io.Writer, in TaskAgentRegisterInput, format types.OutputFormat) (int, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return agentRegisterValidation(
			"A name is required to register a software agent.",
			"The --name flag was missing or empty, and a software identity needs a name.",
			"1. Pass a name with --name:\n"+
				"     pasture task agents register software --name \"pasture-cli\" --version \"0.0.13\"",
			nil,
		)
	}
	version := strings.TrimSpace(in.Version)
	if version == "" {
		return agentRegisterValidation(
			"A version is required to register a software agent.",
			"The --version flag was missing or empty. A software identity records which build it is, so an empty version would be ambiguous.",
			"1. Pass the software version with --version:\n"+
				"     pasture task agents register software --name \"pasture-cli\" --version \"0.0.13\"\n"+
				"2. Optionally record where the software comes from:\n"+
				"     pasture task agents register software --name \"pasture-cli\" --version \"0.0.13\" --source \"github.com/dayvidpham/pasture\"",
			nil,
		)
	}
	ns, code, err := resolveAgentNamespace(in.Namespace)
	if err != nil {
		return code, err
	}
	tr, err := tasks.OpenTaskTracker(in.DBPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tr.Close()

	agent, err := tr.RegisterSoftwareAgent(ns, name, version, strings.TrimSpace(in.Source))
	if err != nil {
		return agentRegisterStoreError("software", err)
	}
	return printRegisteredAgent(w, formatters.AgentEntry{
		AgentId:       agent.ID.String(),
		Kind:          agent.Kind.String(),
		Name:          agent.Name,
		AutomatonRole: protocol.AutomatonRoleNone,
		PastureRole:   protocol.PastureRoleNone,
	}, format)
}

func registerMLAgent(w io.Writer, in TaskAgentRegisterInput, format types.OutputFormat) (int, error) {
	roleText := strings.TrimSpace(in.Role)
	if roleText == "" {
		return agentRegisterValidation(
			"A role is required to register a machine-learning agent.",
			"The --role flag was missing or empty. The role records what part the model plays (architect, supervisor, worker, or reviewer).",
			"1. Pass the role with --role:\n"+
				"     pasture task agents register ml --role worker --provider anthropic --model claude-opus-4-6\n"+
				"2. Valid roles: "+listRoleWireValues(),
			nil,
		)
	}
	var role provenance.Role
	if err := role.UnmarshalText([]byte(roleText)); err != nil {
		return agentRegisterValidation(
			fmt.Sprintf("%q isn't a recognised role for a machine-learning agent.", roleText),
			"The role must name one of the protocol roles.",
			"1. Pass one of the supported roles (case-sensitive):\n"+
				"     "+listRoleWireValues()+"\n"+
				"2. For example:\n"+
				"     pasture task agents register ml --role worker --provider anthropic --model claude-opus-4-6",
			err,
		)
	}

	providerText := strings.TrimSpace(in.Provider)
	if providerText == "" {
		return agentRegisterValidation(
			"A provider is required to register a machine-learning agent.",
			"The --provider flag was missing or empty. The provider names which organisation publishes the model.",
			"1. Pass the provider with --provider:\n"+
				"     pasture task agents register ml --role worker --provider anthropic --model claude-opus-4-6\n"+
				"2. Known providers include: "+listKnownProviders(),
			nil,
		)
	}
	provider := provenance.Provider(providerText)
	if !provider.IsValid() {
		return agentRegisterValidation(
			fmt.Sprintf("%q isn't a recognised model provider.", providerText),
			"The provider must match a provider in the known-model catalog.",
			"1. Pass one of the known providers (case-sensitive):\n"+
				"     "+listKnownProviders()+"\n"+
				"2. Then register with a known model, for example:\n"+
				"     pasture task agents register ml --role worker --provider <provider> --model <model>",
			nil,
		)
	}

	modelText := strings.TrimSpace(in.Model)
	if modelText == "" {
		return agentRegisterValidation(
			"A model is required to register a machine-learning agent.",
			"The --model flag was missing or empty. The model names which model the agent runs.",
			"1. Pass the model with --model:\n"+
				"     pasture task agents register ml --role worker --provider anthropic --model claude-opus-4-6\n"+
				"2. Known models for "+providerText+": "+listProviderModels(provider),
			nil,
		)
	}
	model := provenance.ModelID(modelText)
	registry := provenance.DefaultModelRegistry()
	if _, ok := registry.Lookup(provider, modelText); !ok {
		return agentRegisterValidation(
			fmt.Sprintf("The model %q isn't known for provider %q.", modelText, providerText),
			"A machine-learning identity can only be registered for a model the store knows about, and this provider/model pair isn't in the catalog.",
			"1. Pass a known model for "+providerText+":\n"+
				"     "+listProviderModels(provider)+"\n"+
				"2. Or pick a different provider/model pair:\n"+
				"     pasture task agents register ml --role worker --provider <provider> --model <model>",
			nil,
		)
	}

	ns, code, err := resolveAgentNamespace(in.Namespace)
	if err != nil {
		return code, err
	}
	tr, err := tasks.OpenTaskTracker(in.DBPath)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	defer tr.Close()

	agent, err := tr.RegisterMLAgent(ns, role, provider, model)
	if err != nil {
		return agentRegisterStoreError("machine-learning", err)
	}
	return printRegisteredAgent(w, formatters.AgentEntry{
		AgentId:       agent.ID.String(),
		Kind:          agent.Kind.String(),
		Name:          fmt.Sprintf("%s / %s", role, model),
		AutomatonRole: protocol.AutomatonRoleNone,
		PastureRole:   protocol.PastureRoleNone,
	}, format)
}

// printRegisteredAgent renders the just-created identity. It reuses the
// `agents show` formatter so the printed ID has exactly the same shape the user
// will later paste into `--author`.
func printRegisteredAgent(w io.Writer, entry formatters.AgentEntry, format types.OutputFormat) (int, error) {
	out, err := formatters.FormatAgentEntry(entry, format)
	if err != nil {
		return pasterrors.ExitCode(err), err
	}
	fmt.Fprintln(w, out)
	return 0, nil
}

func resolveAgentNamespace(explicit string) (string, int, error) {
	ns, err := tasks.ResolveNamespace(explicit)
	if err != nil {
		return "", pasterrors.ExitCode(err), err
	}
	return ns, 0, nil
}

// agentRegisterValidation builds a CategoryValidation error for a bad or
// missing registration input.
func agentRegisterValidation(what, why, fix string, cause error) (int, error) {
	se := &pasterrors.StructuredError{
		Category: pasterrors.CategoryValidation,
		What:     what,
		Why:      why,
		Where:    "Registering an author identity (internal/handlers/task_agents_register.go).",
		Impact:   "No author identity was created, so there is no new ID to use with `pasture task comment add --author`.",
		Fix:      fix,
		Cause:    cause,
	}
	return pasterrors.ExitCode(se), se
}

// agentRegisterStoreError wraps a tracker registration failure.
func agentRegisterStoreError(kind string, err error) (int, error) {
	se := &pasterrors.StructuredError{
		Category: pasterrors.CategoryStorage,
		What:     fmt.Sprintf("The %s agent couldn't be registered.", kind),
		Why:      "The store rejected the registration. The most likely causes are listed under \"How to fix\" below.",
		Where:    "Writing the new author identity (internal/handlers/task_agents_register.go).",
		Impact:   "No author identity was created, so there is no new ID to use with `pasture task comment add --author`.",
		Fix: "1. Confirm the --db path is writable and points at a valid store:\n" +
			"     pasture task agents list\n" +
			"2. For a machine-learning agent, confirm the provider and model are known:\n" +
			"     pasture task agents register ml --role worker --provider <provider> --model <model>\n" +
			"3. Re-run the registration after fixing the underlying cause.",
		Cause: err,
	}
	return pasterrors.ExitCode(se), se
}

// listRoleWireValues renders the valid Role wire values for help and errors.
func listRoleWireValues() string {
	values := make([]string, 0, 5)
	for r := provenance.RoleHuman; r <= provenance.RoleReviewer; r++ {
		values = append(values, r.String())
	}
	return strings.Join(values, ", ")
}

// listKnownProviders returns a stable, comma-separated list of provider names
// drawn from the default model catalog. It is deliberately bounded so an error
// message stays readable even though the catalog keeps growing.
func listKnownProviders() string {
	seen := map[provenance.Provider]bool{}
	var providers []string
	for _, m := range provenance.DefaultModelRegistry().Models() {
		if !seen[m.Provider] {
			seen[m.Provider] = true
			providers = append(providers, string(m.Provider))
		}
	}
	sort.Strings(providers)
	return strings.Join(providers, ", ")
}

// listProviderModels renders the known models for one provider as ready-to-run
// `--model` values.
func listProviderModels(provider provenance.Provider) string {
	models := provenance.DefaultModelRegistry().ModelsByProvider(provider)
	values := make([]string, 0, len(models))
	for _, m := range models {
		values = append(values, string(m.Name))
	}
	sort.Strings(values)
	return strings.Join(values, ", ")
}
