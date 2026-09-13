package main

import (
	"context"
	"sort"
	"strings"

	"reasonix/internal/provider"
	"reasonix/internal/sessionv3"
)

func sessionV3Route(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	return remoteSessionIDRoutePrefix + id
}

func parseSessionV3Route(route string) (string, bool) {
	id, ok := strings.CutPrefix(strings.TrimSpace(route), remoteSessionIDRoutePrefix)
	id = strings.TrimSpace(id)
	return id, ok && id != ""
}

func (a *App) listV3SessionsFromDir(dir, active string) []SessionMeta {
	service := a.desktopSessionService(dir)
	if service == nil {
		return []SessionMeta{}
	}
	activeID, _ := parseSessionV3Route(active)
	query := service.Query()
	result := make([]SessionMeta, 0)
	cursor := ""
	for {
		page, err := query.List(cursor, 100)
		if err != nil {
			return result
		}
		for _, info := range page.Sessions {
			meta := SessionMeta{
				Path: sessionV3Route(info.SessionID), SessionID: info.SessionID,
				HostID: service.HostID(), Codec: info.Codec, Error: info.Error,
				Title: info.Title, Turns: info.Turns, TurnsState: "valid",
				CreatedAt: info.CreatedAt.UnixMilli(), LastActivityAt: info.UpdatedAt.UnixMilli(),
				ModTime: info.UpdatedAt.UnixMilli(), Current: info.SessionID == activeID,
			}
			if info.Error != "" {
				meta.TurnsState = "corrupt"
			} else if history, historyErr := query.History(context.Background(), info.Ref); historyErr == nil {
				for _, message := range history {
					if message.Role == provider.RoleUser && strings.TrimSpace(message.Content) != "" {
						meta.Preview = strings.TrimSpace(message.Content)
						break
					}
				}
			}
			a.mu.RLock()
			_, meta.Open = a.runtimeBySessionKey[sessionRuntimeKey(meta.Path)]
			a.mu.RUnlock()
			result = append(result, meta)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].LastActivityAt > result[j].LastActivityAt })
	return result
}

func sessionRefForRoute(service *sessionv3.Service, route string) (sessionv3.SessionRef, bool) {
	id, ok := parseSessionV3Route(route)
	if !ok || service == nil {
		return sessionv3.SessionRef{}, false
	}
	return sessionv3.SessionRef{HostID: service.HostID(), SessionID: id}, true
}
