package envelope

// This file is the seam for SDK <-> core/acp.* type converters
// (FR-015, NFR-010). When the github.com/a2aproject/a2a-go module is
// pinned in go.mod, the unexported helpers below are bound to call
// SDK constructors and accessors. The exported envelope surface
// (envelope.go) does not change.
//
// Until the SDK pin is selected, the helpers are pure-Go shapes that
// model the conversion contract. Tests in convert_test.go assert that
// the conversion never leaks SDK types into a returned core/acp.*
// value.

import (
	"encoding/json"

	"github.com/kameas-ai/kenaz-harness/core/acp"
)

// sdkCard mirrors the shape of an A2A v1.0 AgentCard. When the SDK is
// pinned this type is replaced with the SDK's AgentCard type and the
// converter functions below are rebound. v1 keeps the shape
// internal-only so no SDK types leak across the package boundary.
type sdkCard struct {
	Name            string
	Description     string
	Version         string
	ProtocolVersion string
	EndpointURL     string
	Skills          []sdkSkill
	AuthSchemes     []string
}

type sdkSkill struct {
	ID           string
	Name         string
	Description  string
	InputSchema  json.RawMessage
	OutputSchema json.RawMessage
	Examples     []json.RawMessage
	Tags         []string
}

// fromSDKCard converts an SDK AgentCard into a core/acp AgentCard.
func fromSDKCard(s sdkCard, agentID string) acp.AgentCard {
	skills := make([]acp.Skill, 0, len(s.Skills))
	for _, sk := range s.Skills {
		skills = append(skills, acp.Skill{
			ID:           sk.ID,
			Name:         sk.Name,
			Description:  sk.Description,
			InputSchema:  sk.InputSchema,
			OutputSchema: sk.OutputSchema,
			Examples:     sk.Examples,
			Tags:         sk.Tags,
		})
	}
	pv := s.ProtocolVersion
	if pv == "" {
		pv = acp.ProtocolVersion
	}
	return acp.AgentCard{
		AgentID:         agentID,
		Name:            s.Name,
		Description:     s.Description,
		Version:         s.Version,
		ProtocolVersion: pv,
		EndpointURL:     s.EndpointURL,
		Skills:          skills,
		AuthSchemes:     s.AuthSchemes,
		Signed:          false, // v1: always false; signing follows in trust mission.
	}
}

// toSDKCard converts a core/acp AgentCard into the shape an A2A SDK
// AgentCard takes. Caller side is responsible for marshalling onto the
// wire.
func toSDKCard(c acp.AgentCard) sdkCard {
	skills := make([]sdkSkill, 0, len(c.Skills))
	for _, sk := range c.Skills {
		skills = append(skills, sdkSkill{
			ID:           sk.ID,
			Name:         sk.Name,
			Description:  sk.Description,
			InputSchema:  sk.InputSchema,
			OutputSchema: sk.OutputSchema,
			Examples:     sk.Examples,
			Tags:         sk.Tags,
		})
	}
	return sdkCard{
		Name:            c.Name,
		Description:     c.Description,
		Version:         c.Version,
		ProtocolVersion: c.ProtocolVersion,
		EndpointURL:     c.EndpointURL,
		Skills:          skills,
		AuthSchemes:     c.AuthSchemes,
	}
}

// sdkMessage mirrors the shape of an A2A wire Message. Replaced with
// the SDK's Message type when the pin is selected.
type sdkMessage struct {
	TaskID   string
	Kind     string
	Payload  json.RawMessage
	Sequence int
}

func toSDKMessage(m acp.Message) sdkMessage {
	return sdkMessage{
		TaskID:   m.TaskID,
		Kind:     string(m.Kind),
		Payload:  m.Payload,
		Sequence: m.Sequence,
	}
}

func fromSDKMessage(s sdkMessage, dir acp.MessageDirection) acp.Message {
	return acp.Message{
		MessageID: newMessageID(),
		TaskID:    s.TaskID,
		Direction: dir,
		Kind:      acp.MessageKind(s.Kind),
		Payload:   s.Payload,
		Sequence:  s.Sequence,
	}
}
