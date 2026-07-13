// Explicit registry for all generated skill body content.
//
// Each body declaration lives in its own specs_data_body_<skill>.go file so
// skill edits remain isolated, while this map provides a single visible
// inventory without hidden init-time registration.
//
// Keys are skill directory names (not protocol.RoleId) because sub-skills like
// "supervisor-plan-tasks" have no RoleId equivalent.
package codegen

var SkillBodySpecs = map[string]SkillBody{
	"architect":                 architectBody,
	"architect-handoff":         architectHandoffBody,
	"architect-propose-plan":    architectProposePlanBody,
	"architect-ratify":          architectRatifyBody,
	"architect-request-review":  architectRequestReviewBody,
	"epoch":                     epochBody,
	"explore":                   exploreBody,
	"impl-review":               implReviewBody,
	"impl-slice":                implSliceBody,
	"research":                  researchBody,
	"reviewer":                  reviewerBody,
	"reviewer-comment":          reviewerCommentBody,
	"reviewer-review-code":      reviewerReviewCodeBody,
	"reviewer-review-plan":      reviewerReviewPlanBody,
	"reviewer-vote":             reviewerVoteBody,
	"status":                    statusBody,
	"supervisor":                supervisorBody,
	"supervisor-commit":         supervisorCommitBody,
	"supervisor-plan-tasks":     supervisorPlanTasksBody,
	"supervisor-spawn-worker":   supervisorSpawnWorkerBody,
	"supervisor-track-progress": supervisorTrackProgressBody,
	"swarm":                     swarmBody,
	"user-elicit":               userElicitBody,
	"user-request":              userRequestBody,
	"user-uat":                  userUatBody,
	"worker":                    workerBody,
	"worker-blocked":            workerBlockedBody,
	"worker-complete":           workerCompleteBody,
	"worker-implement":          workerImplementBody,
}
