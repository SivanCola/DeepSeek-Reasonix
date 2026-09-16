package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/provider"
	"reasonix/internal/sessioncatalog"
)

const (
	aiSessionTitleMaxTurns     = 3
	aiSessionTitleMaxTurnRunes = 500
)

// AIRenameSession generates a title from the explicitly targeted durable
// conversation. The argument also accepts a canonical session route or legacy
// path, which takes precedence over the compatibility topic lookup.
func (a *App) AIRenameSession(topicID string) (string, error) {
	topicID = strings.TrimSpace(topicID)
	if topicID == "" {
		return "", newSessionOperationError(sessionOperationTargetNotFound, "The session no longer exists.")
	}
	selector := sessionTargetSelector{TopicID: topicID}
	if _, ok := parseSessionRoute(topicID); ok || strings.ContainsAny(topicID, `/\`) {
		selector = sessionTargetSelector{SessionPath: topicID}
	}
	target, err := a.resolveSessionTarget(selector)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(target.SessionRef.SessionID) == "" && strings.TrimSpace(target.SessionPath) == "" {
		return "", newSessionOperationError(sessionOperationNoMessages, "This session has no user messages to analyze.")
	}
	key := target.key()
	a.aiSessionTitleMu.Lock()
	if a.aiSessionTitleInFlight == nil {
		a.aiSessionTitleInFlight = map[string]struct{}{}
	}
	if _, busy := a.aiSessionTitleInFlight[key]; busy {
		a.aiSessionTitleMu.Unlock()
		return "", newSessionOperationError(sessionOperationBusy, "AI rename is already running for this session.")
	}
	a.aiSessionTitleInFlight[key] = struct{}{}
	a.aiSessionTitleMu.Unlock()
	defer func() {
		a.aiSessionTitleMu.Lock()
		delete(a.aiSessionTitleInFlight, key)
		a.aiSessionTitleMu.Unlock()
	}()

	if strings.TrimSpace(target.SessionRef.SessionID) != "" {
		return a.aiRenameCanonicalSession(target)
	}
	return a.aiRenameLegacySession(target)
}

func (a *App) aiRenameLegacySession(target SessionTarget) (string, error) {
	sessionDir, validated, err := a.sessionDirForPath(target.SessionPath)
	if err != nil {
		return "", newSessionOperationError(sessionOperationTargetNotFound, "The session no longer exists.")
	}
	expectedTitle := ""
	modelRef := ""
	if meta, ok, loadErr := agent.LoadBranchMeta(validated); loadErr != nil {
		return "", fmt.Errorf("AI rename session: read current title: %w", loadErr)
	} else if ok {
		expectedTitle = meta.CustomTitle
		modelRef = meta.Model
	}
	users, err := loadTopicTitleUserTurnsFromSession(validated)
	if err != nil {
		return "", fmt.Errorf("AI rename session: read conversation: %w", err)
	}
	if len(users) == 0 {
		return "", newSessionOperationError(sessionOperationNoMessages, "This session has no user messages to analyze.")
	}
	title, err := a.generateTargetSessionTitle(a.bootContext(), target, modelRef, sessionTitleTranscript(users))
	if err != nil {
		return "", err
	}
	if target.IsOpen && target.Controller != nil && !a.topicControllerOwnsSession(target.TopicID, target.Controller, validated) {
		return "", fmt.Errorf("session changed while AI rename was running; try again")
	}
	a.sessionRemovalMu.Lock()
	defer a.sessionRemovalMu.Unlock()
	a.topicTitleMutationMu.Lock()
	defer a.topicTitleMutationMu.Unlock()
	if err := a.renameSessionInDirIfTitleUnchanged(sessionDir, validated, expectedTitle, title); err != nil {
		if errors.Is(err, agent.ErrSessionTitleChanged) {
			return "", newSessionOperationError(sessionOperationTitleConflict, "The session title changed while AI rename was running. Try again.")
		}
		return "", err
	}
	return title, nil
}

func (a *App) aiRenameCanonicalSession(target SessionTarget) (string, error) {
	service := a.desktopSessionService("")
	ref := target.SessionRef
	var topicRoot string
	var hasTopic bool
	if target.TopicID != "" && !target.SharedTopic {
		topicRoot = topicTitleRoot(target.Scope, target.WorkspaceRoot)
		hasTopic = true
	}
	expectedTopicTitle := ""
	if hasTopic {
		expectedTopicTitle = loadTopicTitle(topicRoot, target.TopicID)
	}
	ctx, cancel := context.WithTimeout(a.bootContext(), 30*time.Second)
	defer cancel()
	info, err := service.Query().Stat(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("AI rename session: read current title: %w", err)
	}
	// Publish accepted user turns before querying durable history when the
	// target has a live runtime. Cold sessions are already fully durable.
	if runtime, ok := service.Runtime(ref); ok {
		if _, err := runtime.Session().Flush(ctx); err != nil {
			return "", fmt.Errorf("AI rename session: flush conversation: %w", err)
		}
	}
	messages, err := service.Query().TitleMessages(ctx, ref, aiSessionTitleMaxTurns)
	if err != nil {
		return "", fmt.Errorf("AI rename session: read conversation: %w", err)
	}
	var users []string
	for _, message := range messages {
		if content := topicTitleUserText(message); content != "" {
			users = append(users, content)
		}
	}
	if len(users) == 0 {
		return "", newSessionOperationError(sessionOperationNoMessages, "This session has no user messages to analyze.")
	}
	title, err := a.generateTargetSessionTitle(ctx, target, info.ModelRef, sessionTitleTranscript(users))
	if err != nil {
		return "", err
	}
	// The legacy sidebar still owns a topic label. Serialize its projection
	// with manual/automatic topic renames, as well as guarding the session title.
	a.sessionRemovalMu.Lock()
	defer a.sessionRemovalMu.Unlock()
	a.topicTitleMutationMu.Lock()
	defer a.topicTitleMutationMu.Unlock()
	if target.IsOpen && target.Controller != nil {
		if !a.sessionTargetStillOwnsRuntime(target) {
			return "", fmt.Errorf("session changed while AI rename was running; try again")
		}
	}
	if hasTopic && loadTopicTitle(topicRoot, target.TopicID) != expectedTopicTitle {
		return "", newSessionOperationError(sessionOperationTitleConflict, "The session title changed while AI rename was running. Try again.")
	}
	if err := a.workspaceRegistry().WithSessionUnchanged(ctx, ref.SessionID, target.WorkspaceID, target.LifecycleGeneration, func() error {
		return service.SetTitleIfUnchanged(ctx, ref, info.Title, title)
	}); err != nil {
		return "", sessionOperationConflict(err)
	}
	if hasTopic {
		if err := setTopicTitle(topicRoot, target.TopicID, title); err != nil {
			return "", fmt.Errorf("AI rename session: update topic title: %w", err)
		}
		a.updateOpenTopicTitle(target.TopicID, title, topicTitleSourceManual)
	}
	a.invalidatePromptHistoryCache()
	a.emitProjectTreeChanged()
	return title, nil
}

func (a *App) generateTargetSessionTitle(ctx context.Context, target SessionTarget, modelRef, transcript string) (string, error) {
	if target.Controller != nil {
		return target.Controller.GenerateSessionTitleForModel(ctx, modelRef, transcript)
	}
	root := target.WorkspaceRoot
	if target.Scope == "global" || root == "" {
		root = globalWorkspaceRoot()
	}
	cfg, err := config.LoadModelRuntimeSnapshot(root, modelRef)
	if err != nil {
		return "", newSessionOperationError("provider_unavailable", "Unable to load model settings. Check the session's provider configuration.")
	}
	if modelRef == "" {
		modelRef = cfg.DefaultModel
	}
	return control.GenerateSessionTitleWithResolver(ctx, boot.NewLocalProviderResolver(cfg, cfg.NetworkProxySpec()), modelRef, transcript)
}

func (a *App) controllerForTopic(topicID string) *control.Controller {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var found *control.Controller
	for _, tab := range a.runtimeTabsLocked() {
		if tab == nil || (strings.TrimSpace(tab.TopicID) != topicID && tab.SessionID != topicID && sessionRoute(tab.SessionID) != topicID) || tab.Ctrl == nil {
			continue
		}
		if ctrl, ok := tab.Ctrl.(*control.Controller); ok {
			if tab.ID == a.activeTabID {
				return ctrl
			}
			if found != nil && found != ctrl {
				return nil
			}
			found = ctrl
		}
	}
	return found
}

func (a *App) topicControllerOwnsSession(topicID string, ctrl *control.Controller, sessionPath string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, tab := range a.runtimeTabsLocked() {
		if tab == nil || tab.TopicID != topicID || tab.Ctrl != ctrl {
			continue
		}
		return sessionRuntimeKey(tab.currentSessionPath()) == sessionRuntimeKey(sessionPath)
	}
	return false
}

func topicTitleUserText(message provider.Message) string {
	if !agent.IsUserAuthoredTurnMessage(message) {
		return ""
	}
	content := control.StripComposePrefixes(agent.UserPreviewText(agent.UserMessageText(message)))
	return strings.TrimSpace(control.StripReferencedContextPrefix(content))
}

func sessionTitleTranscript(users []string) string {
	parts := make([]string, 0, aiSessionTitleMaxTurns)
	for _, user := range users {
		if len(parts) >= aiSessionTitleMaxTurns {
			break
		}
		user = strings.TrimSpace(user)
		if runes := []rune(user); len(runes) > aiSessionTitleMaxTurnRunes {
			user = string(runes[:aiSessionTitleMaxTurnRunes])
		}
		if user != "" {
			parts = append(parts, user)
		}
	}
	return strings.Join(parts, "\n\n")
}

func sessionPreviewForPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if meta, ok, err := agent.LoadBranchMeta(path); err == nil && ok {
		if preview := strings.TrimSpace(meta.Preview); preview != "" {
			return preview
		}
	}
	preview, ok, err := agent.LoadSessionPreviewFromDisplayIndex(path)
	if err != nil || !ok {
		return ""
	}
	return preview
}

func topicSessionPreview(sessions []sessioncatalog.SessionRecord, path string) string {
	for _, session := range sessions {
		if sessionRuntimeKey(session.Path) == sessionRuntimeKey(path) {
			return strings.TrimSpace(session.Preview)
		}
	}
	return ""
}
