package main

import (
	"net/http"

	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/api/types/endpoint"
	"github.com/rulego/rulego/builtin/processor"
)

const responseProcessor = "resourceOriginResponse"

// Plugins is the symbol loaded by RuleGo's Go plugin registry.
var Plugins pluginRegistry

type pluginRegistry struct{}

func (*pluginRegistry) Init() error { return nil }

func (*pluginRegistry) Components() []types.Node {
	return []types.Node{&resourceOriginNode{}}
}

func init() {
	// The pinned plugin loader loads Components but does not call Init.
	processor.OutBuiltins.Register(responseProcessor, projectOriginResponse)
}

func projectOriginResponse(_ endpoint.Router, exchange *endpoint.Exchange) bool {
	exchange.Lock()
	defer exchange.Unlock()
	msg := exchange.Out.GetMsg()
	status := http.StatusOK
	if exchange.Out.GetError() != nil {
		kind := "internal"
		if msg != nil && msg.Metadata != nil && msg.Metadata.GetValue(metadataKind) != "" {
			kind = msg.Metadata.GetValue(metadataKind)
		}
		status = statusForError(kind)
	} else {
		state := ""
		if msg != nil && msg.Metadata != nil {
			state = msg.Metadata.GetValue(metadataState)
		}
		switch resourceState(state) {
		case stateReady:
			status = http.StatusTemporaryRedirect
			if location := msg.Metadata.GetValue(metadataURL); location != "" {
				exchange.Out.Headers().Set("Location", location)
			}
		case statePending:
			status = http.StatusAccepted
		case stateFailed:
			status = http.StatusBadGateway
		case stateExpired:
			status = http.StatusGone
		case stateNotFound:
			status = http.StatusNotFound
		}
	}
	if msg != nil {
		exchange.Out.Headers().Set("Content-Type", "application/json")
	}
	exchange.Out.SetStatusCode(status)
	if msg != nil {
		exchange.Out.SetBody(msg.GetBytes())
	}
	return true
}

func statusForError(kind string) int {
	switch kind {
	case "invalid_input":
		return http.StatusBadRequest
	case "stale_generation", "conflict", "parent_unavailable", "invalid_publication":
		return http.StatusConflict
	case "storage_limit":
		return http.StatusInsufficientStorage
	case "wait_timeout", "production_timeout":
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}
