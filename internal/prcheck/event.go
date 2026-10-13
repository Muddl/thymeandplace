package prcheck

func SkipGovernance(githubEventName string) bool {
	return githubEventName == "merge_group"
}
