package gtm

import "fmt"

func accountPath(accountID string) string {
	return fmt.Sprintf("accounts/%s", accountID)
}

func containerPath(accountID, containerID string) string {
	return fmt.Sprintf("accounts/%s/containers/%s", accountID, containerID)
}

func workspacePath(accountID, containerID, workspaceID string) string {
	return fmt.Sprintf("accounts/%s/containers/%s/workspaces/%s", accountID, containerID, workspaceID)
}

func tagPath(accountID, containerID, workspaceID, tagID string) string {
	return fmt.Sprintf("%s/tags/%s", workspacePath(accountID, containerID, workspaceID), tagID)
}

func triggerPath(accountID, containerID, workspaceID, triggerID string) string {
	return fmt.Sprintf("%s/triggers/%s", workspacePath(accountID, containerID, workspaceID), triggerID)
}

func variablePath(accountID, containerID, workspaceID, variableID string) string {
	return fmt.Sprintf("%s/variables/%s", workspacePath(accountID, containerID, workspaceID), variableID)
}

func builtInVariablesPath(accountID, containerID, workspaceID string) string {
	return fmt.Sprintf("%s/built_in_variables", workspacePath(accountID, containerID, workspaceID))
}

func folderPath(accountID, containerID, workspaceID, folderID string) string {
	return fmt.Sprintf("%s/folders/%s", workspacePath(accountID, containerID, workspaceID), folderID)
}

func templatePath(accountID, containerID, workspaceID, templateID string) string {
	return fmt.Sprintf("%s/templates/%s", workspacePath(accountID, containerID, workspaceID), templateID)
}

func clientPath(accountID, containerID, workspaceID, clientID string) string {
	return fmt.Sprintf("%s/clients/%s", workspacePath(accountID, containerID, workspaceID), clientID)
}

func transformationPath(accountID, containerID, workspaceID, transformationID string) string {
	return fmt.Sprintf("%s/transformations/%s", workspacePath(accountID, containerID, workspaceID), transformationID)
}

func versionPath(accountID, containerID, versionID string) string {
	return fmt.Sprintf("%s/versions/%s", containerPath(accountID, containerID), versionID)
}
