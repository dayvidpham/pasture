// The legacy orchestration entry remains discoverable only to direct users to
// the supported role workflow; it does not execute deprecated orchestration.
package codegen

var swarmBody = SkillBody{
	Preamble: "Deprecated orchestration entry. Do not invoke aura-swarm or its session registry. Use the supervisor and worker role workflow in isolated issue-named Git worktrees.",
	Sections: []ProseSection{
		fragRef(FragTaskRecovery),
		fragRef(FragTaskAuthor),
		{
			Id:      "swarm-retirement",
			Title:   "Supported Workflow",
			Content: "Read the assigned implementation plan and full task URIs from Pasture. The supervisor assigns vertical slices; workers own their production paths and report evidence. Follow the repository's Git isolation, review, and landing rules. Do not start the deprecated orchestrator or introduce a replacement Python protocol engine.",
		},
	},
}
