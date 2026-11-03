package claude

// The claude.ai connectors' write tools, by exact name. A run that only reads denies them by name
// (deny beats any permission rule added later), so a rule that one day allows a connector's tools
// cannot open them to a run that reads untrusted text. The allow list and dontAsk already keep
// every other tool out; this is the backstop. A name a connector does not have, or a connector
// that is not signed in, denies nothing and costs nothing. Exact names only: a rule for a whole
// server is not relied on.

// SlackWrite are the Slack connector's tools that send, change or upload something.
func SlackWrite() []string {
	return connectorTools("Slack", []string{
		"slack_send_message", "slack_send_message_draft", "slack_schedule_message",
		"slack_add_reaction", "slack_create_canvas", "slack_update_canvas",
		"slack_create_conversation", "slack_create_list", "slack_add_list_record",
		"slack_update_list", "slack_update_list_record", "slack_get_file_upload_url",
		"slack_complete_file_upload",
	})
}

// OtherConnectorWrite are the other connectors' tools that create, change, share or delete
// something: Jira, Confluence, Compass, Drive files and Docs.
func OtherConnectorWrite() []string {
	tools := connectorTools("Atlassian", []string{
		"addCommentToJiraIssue", "addWorklogToJiraIssue", "addTeamworkGraphContext",
		"createCompassComponent", "createCompassComponentRelationship",
		"createCompassCustomFieldDefinition", "createConfluenceFooterComment",
		"createConfluenceInlineComment", "createConfluencePage", "createIssueLink",
		"createJiraIssue", "editJiraIssue", "transitionJiraIssue", "updateConfluencePage",
	})
	tools = append(tools, connectorTools("Atlassian_MCP", []string{
		"addGraphContext", "addOrEditJiraIssueComment", "createConfluenceContent",
		"createJiraIssue", "editJiraIssue", "executeDestructive", "executeWrite",
		"transitionJiraIssue", "updateConfluenceContent",
	})...)
	tools = append(tools, connectorTools("Google_Drive", []string{
		"copy_file", "create_file", "share_file", "trash_file", "update_file",
	})...)

	return append(tools, connectorTools("Google_Docs", []string{"update_doc"})...)
}

// connectorTools are a claude.ai connector's tool names: mcp__claude_ai_<server>__<name>.
func connectorTools(server string, names []string) []string {
	tools := make([]string, 0, len(names))
	for _, name := range names {
		tools = append(tools, "mcp__claude_ai_"+server+"__"+name)
	}

	return tools
}
