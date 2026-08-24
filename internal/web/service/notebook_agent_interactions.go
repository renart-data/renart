package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	maxNotebookAgentQuestions       = 3
	maxNotebookAgentQuestionOptions = 8
	maxNotebookAgentQuestionText    = 2 << 10
)

type NotebookAgentInteractionKind string

const (
	NotebookAgentInteractionQuestionnaire NotebookAgentInteractionKind = "questionnaire"
	NotebookAgentInteractionConnection    NotebookAgentInteractionKind = "connection_access"
)

type NotebookAgentQuestionKind string

const (
	NotebookAgentQuestionSingle   NotebookAgentQuestionKind = "single_choice"
	NotebookAgentQuestionMultiple NotebookAgentQuestionKind = "multiple_choice"
	NotebookAgentQuestionText     NotebookAgentQuestionKind = "text"
)

type NotebookAgentQuestionOption struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
}

type NotebookAgentQuestion struct {
	ID          string                        `json:"id"`
	Kind        NotebookAgentQuestionKind     `json:"kind"`
	Prompt      string                        `json:"prompt"`
	Description string                        `json:"description,omitempty"`
	Required    bool                          `json:"required,omitempty"`
	Options     []NotebookAgentQuestionOption `json:"options,omitempty"`
}

type NotebookAgentQuestionnaireRequest struct {
	Title       string                  `json:"title"`
	Description string                  `json:"description,omitempty"`
	Questions   []NotebookAgentQuestion `json:"questions"`
}

type NotebookAgentQuestionAnswer struct {
	QuestionID string   `json:"question_id"`
	Values     []string `json:"values,omitempty"`
	Text       string   `json:"text,omitempty"`
}

type NotebookAgentConnectionCapability string

const (
	NotebookAgentConnectionDiscover    NotebookAgentConnectionCapability = "discover"
	NotebookAgentConnectionSampleQuery NotebookAgentConnectionCapability = "sample_query"
)

type NotebookAgentConnectionAccessRequest struct {
	Title          string                              `json:"title"`
	Description    string                              `json:"description,omitempty"`
	ConnectionName string                              `json:"connection_name,omitempty"`
	ConnectionType string                              `json:"connection_type,omitempty"`
	Capabilities   []NotebookAgentConnectionCapability `json:"capabilities,omitempty"`
}

type NotebookAgentQueryConnection struct {
	Name           string                              `json:"name"`
	ConnectionType string                              `json:"connection_type"`
	AssetType      string                              `json:"asset_type"`
	Dialect        string                              `json:"dialect"`
	Environment    string                              `json:"environment"`
	Capabilities   []NotebookAgentConnectionCapability `json:"capabilities"`
	Granted        bool                                `json:"granted"`
}

type NotebookAgentConnectionGrant struct {
	NotebookID     string
	TurnID         string
	ConnectionName string
	Environment    string
	Capabilities   []NotebookAgentConnectionCapability
	ExpiresAt      time.Time
}

type NotebookAgentInteraction struct {
	ID                string                                `json:"id"`
	TurnID            string                                `json:"turn_id"`
	Kind              NotebookAgentInteractionKind          `json:"kind"`
	Status            string                                `json:"status"`
	Title             string                                `json:"title"`
	Description       string                                `json:"description,omitempty"`
	Questions         []NotebookAgentQuestion               `json:"questions,omitempty"`
	Answers           []NotebookAgentQuestionAnswer         `json:"answers,omitempty"`
	ConnectionRequest *NotebookAgentConnectionAccessRequest `json:"connection_request,omitempty"`
	Connection        *NotebookAgentQueryConnection         `json:"connection,omitempty"`
	CreatedAt         string                                `json:"created_at"`
	FinishedAt        string                                `json:"finished_at,omitempty"`
}

type AnswerNotebookAgentInteractionRequest struct {
	Answers        []NotebookAgentQuestionAnswer `json:"answers,omitempty"`
	ConnectionName string                        `json:"connection_name,omitempty"`
	Declined       bool                          `json:"declined,omitempty"`
}

type NotebookAgentInteractionResult struct {
	Status     string                        `json:"status"`
	Answers    []NotebookAgentQuestionAnswer `json:"answers,omitempty"`
	Connection *NotebookAgentQueryConnection `json:"connection,omitempty"`
}

type notebookAgentPendingInteraction struct {
	id     string
	turnID string
	result chan NotebookAgentInteractionResult
}

// RequestQuestionnaire is the native MCP boundary. The unguessable token is
// bound to one active notebook turn and is never returned in snapshots.
func (s *NotebookAgentService) RequestQuestionnaire(
	ctx context.Context,
	notebookID string,
	turnToken string,
	request NotebookAgentQuestionnaireRequest,
) (NotebookAgentInteractionResult, *APIError) {
	notebookID = strings.TrimSpace(notebookID)
	turnToken = strings.TrimSpace(turnToken)
	if apiErr := validateNotebookAgentQuestionnaire(request); apiErr != nil {
		return NotebookAgentInteractionResult{}, apiErr
	}

	now := s.deps.Now().UTC().Format(time.RFC3339Nano)
	s.mu.Lock()
	conversation := s.items[notebookID]
	if conversation == nil || conversation.snapshot.Status != "running" || conversation.activeTurn == "" || conversation.activeToken != turnToken {
		s.mu.Unlock()
		return NotebookAgentInteractionResult{}, &APIError{
			Status:  http.StatusConflict,
			Code:    "notebook_agent_turn_token_invalid",
			Message: "this notebook agent interaction does not belong to the active turn",
		}
	}
	if conversation.pendingInteraction != nil {
		s.mu.Unlock()
		return NotebookAgentInteractionResult{}, &APIError{
			Status:  http.StatusConflict,
			Code:    "notebook_agent_interaction_pending",
			Message: "answer the current notebook agent question before another is opened",
		}
	}
	interactionID := s.deps.NewID()
	pending := &notebookAgentPendingInteraction{
		id: interactionID, turnID: conversation.activeTurn,
		result: make(chan NotebookAgentInteractionResult, 1),
	}
	conversation.pendingInteraction = pending
	conversation.snapshot.Interaction = &NotebookAgentInteraction{
		ID: interactionID, TurnID: conversation.activeTurn,
		Kind: NotebookAgentInteractionQuestionnaire, Status: "pending",
		Title: strings.TrimSpace(request.Title), Description: strings.TrimSpace(request.Description),
		Questions: cloneNotebookAgentQuestions(request.Questions), CreatedAt: now,
	}
	conversation.snapshot.Revision++
	snapshot := cloneNotebookAgentSnapshot(conversation.snapshot)
	s.mu.Unlock()
	s.publish(snapshot)

	select {
	case result := <-pending.result:
		return result, nil
	case <-ctx.Done():
		s.cancelPendingInteraction(notebookID, pending.id, pending.turnID)
		return NotebookAgentInteractionResult{}, &APIError{
			Status:  http.StatusRequestTimeout,
			Code:    "notebook_agent_interaction_cancelled",
			Message: "the notebook agent question was cancelled",
		}
	}
}

func (s *NotebookAgentService) AnswerInteraction(
	notebookID string,
	interactionID string,
	request AnswerNotebookAgentInteractionRequest,
) (NotebookAgentSnapshot, *APIError) {
	notebookID = strings.TrimSpace(notebookID)
	interactionID = strings.TrimSpace(interactionID)
	if apiErr := s.validateNotebook(notebookID); apiErr != nil {
		return NotebookAgentSnapshot{}, apiErr
	}

	s.mu.Lock()
	conversation := s.items[notebookID]
	if conversation == nil || conversation.pendingInteraction == nil ||
		conversation.pendingInteraction.id != interactionID || conversation.snapshot.Interaction == nil {
		s.mu.Unlock()
		return NotebookAgentSnapshot{}, &APIError{
			Status:  http.StatusConflict,
			Code:    "notebook_agent_interaction_stale",
			Message: "this notebook agent question is no longer awaiting an answer",
		}
	}
	interaction := conversation.snapshot.Interaction
	if request.Declined && (len(request.Answers) != 0 || strings.TrimSpace(request.ConnectionName) != "") {
		s.mu.Unlock()
		return NotebookAgentSnapshot{}, badRequestError("notebook_agent_interaction_answer_invalid", "a declined interaction cannot include an answer")
	}
	var answers []NotebookAgentQuestionAnswer
	var approvedConnection *NotebookAgentQueryConnection
	switch interaction.Kind {
	case NotebookAgentInteractionQuestionnaire:
		if strings.TrimSpace(request.ConnectionName) != "" {
			s.mu.Unlock()
			return NotebookAgentSnapshot{}, badRequestError("notebook_agent_interaction_answer_invalid", "a questionnaire answer cannot approve a connection")
		}
		var apiErr *APIError
		answers, apiErr = validateNotebookAgentAnswers(interaction.Questions, request)
		if apiErr != nil {
			s.mu.Unlock()
			return NotebookAgentSnapshot{}, apiErr
		}
	case NotebookAgentInteractionConnection:
		if len(request.Answers) != 0 {
			s.mu.Unlock()
			return NotebookAgentSnapshot{}, badRequestError("notebook_agent_interaction_answer_invalid", "a connection approval cannot include questionnaire answers")
		}
		if !request.Declined {
			connection, grant, apiErr := s.validateNotebookAgentConnectionAnswerLocked(
				notebookID,
				conversation,
				strings.TrimSpace(request.ConnectionName),
			)
			if apiErr != nil {
				s.mu.Unlock()
				return NotebookAgentSnapshot{}, apiErr
			}
			approvedConnection = &connection
			conversation.connectionGrants[connection.Name] = grant
		}
	default:
		s.mu.Unlock()
		return NotebookAgentSnapshot{}, &APIError{Status: http.StatusConflict, Code: "notebook_agent_interaction_invalid", Message: "this notebook agent interaction has an unsupported kind"}
	}
	status := "answered"
	resultStatus := "answered"
	if request.Declined {
		status = "declined"
		resultStatus = "declined"
	}
	now := s.deps.Now().UTC().Format(time.RFC3339Nano)
	conversation.snapshot.Interaction.Status = status
	conversation.snapshot.Interaction.Answers = answers
	conversation.snapshot.Interaction.Connection = approvedConnection
	conversation.snapshot.Interaction.FinishedAt = now
	pending := conversation.pendingInteraction
	conversation.pendingInteraction = nil
	conversation.snapshot.Revision++
	snapshot := cloneNotebookAgentSnapshot(conversation.snapshot)
	s.mu.Unlock()
	pending.result <- NotebookAgentInteractionResult{
		Status: resultStatus, Answers: answers, Connection: approvedConnection,
	}
	s.publish(snapshot)
	return snapshot, nil
}

func (s *NotebookAgentService) cancelPendingInteraction(notebookID, interactionID, turnID string) {
	s.mu.Lock()
	conversation := s.items[notebookID]
	if conversation == nil || conversation.pendingInteraction == nil ||
		conversation.pendingInteraction.id != interactionID ||
		conversation.pendingInteraction.turnID != turnID {
		s.mu.Unlock()
		return
	}
	s.cancelPendingInteractionLocked(conversation, "cancelled")
	conversation.snapshot.Revision++
	snapshot := cloneNotebookAgentSnapshot(conversation.snapshot)
	s.mu.Unlock()
	s.publish(snapshot)
}

func (s *NotebookAgentService) cancelPendingInteractionLocked(
	conversation *notebookAgentConversation,
	status string,
) {
	pending := conversation.pendingInteraction
	if pending == nil {
		return
	}
	conversation.pendingInteraction = nil
	if conversation.snapshot.Interaction != nil && conversation.snapshot.Interaction.ID == pending.id {
		conversation.snapshot.Interaction.Status = status
		conversation.snapshot.Interaction.FinishedAt = s.deps.Now().UTC().Format(time.RFC3339Nano)
	}
	pending.result <- NotebookAgentInteractionResult{Status: status}
}

func validateNotebookAgentQuestionnaire(request NotebookAgentQuestionnaireRequest) *APIError {
	if title := strings.TrimSpace(request.Title); title == "" || len(title) > 120 {
		return badRequestError("notebook_agent_questionnaire_title_invalid", "questionnaire title must contain 1 to 120 characters")
	}
	if len(request.Description) > 1<<10 {
		return badRequestError("notebook_agent_questionnaire_description_invalid", "questionnaire description may not exceed 1024 characters")
	}
	if len(request.Questions) == 0 || len(request.Questions) > maxNotebookAgentQuestions {
		return badRequestError("notebook_agent_questionnaire_size_invalid", fmt.Sprintf("questionnaires must contain 1 to %d questions", maxNotebookAgentQuestions))
	}
	seen := make(map[string]struct{}, len(request.Questions))
	for _, question := range request.Questions {
		if !notebookAgentIdentifier(question.ID) {
			return badRequestError("notebook_agent_question_id_invalid", "question ids must use lowercase letters, numbers, underscores, or hyphens")
		}
		if _, exists := seen[question.ID]; exists {
			return badRequestError("notebook_agent_question_id_duplicate", fmt.Sprintf("question id %q is duplicated", question.ID))
		}
		seen[question.ID] = struct{}{}
		if prompt := strings.TrimSpace(question.Prompt); prompt == "" || len(prompt) > 500 {
			return badRequestError("notebook_agent_question_prompt_invalid", fmt.Sprintf("question %q must have a prompt of at most 500 characters", question.ID))
		}
		if len(question.Description) > 1<<10 {
			return badRequestError("notebook_agent_question_description_invalid", fmt.Sprintf("question %q description may not exceed 1024 characters", question.ID))
		}
		switch question.Kind {
		case NotebookAgentQuestionText:
			if len(question.Options) != 0 {
				return badRequestError("notebook_agent_question_options_invalid", fmt.Sprintf("text question %q cannot define options", question.ID))
			}
		case NotebookAgentQuestionSingle, NotebookAgentQuestionMultiple:
			if len(question.Options) < 2 || len(question.Options) > maxNotebookAgentQuestionOptions {
				return badRequestError("notebook_agent_question_options_invalid", fmt.Sprintf("choice question %q must define 2 to %d options", question.ID, maxNotebookAgentQuestionOptions))
			}
			optionIDs := make(map[string]struct{}, len(question.Options))
			for _, option := range question.Options {
				if !notebookAgentIdentifier(option.Value) || strings.TrimSpace(option.Label) == "" || len(option.Label) > 160 || len(option.Description) > 500 {
					return badRequestError("notebook_agent_question_option_invalid", fmt.Sprintf("question %q contains an invalid option", question.ID))
				}
				if _, exists := optionIDs[option.Value]; exists {
					return badRequestError("notebook_agent_question_option_duplicate", fmt.Sprintf("question %q option %q is duplicated", question.ID, option.Value))
				}
				optionIDs[option.Value] = struct{}{}
			}
		default:
			return badRequestError("notebook_agent_question_kind_invalid", fmt.Sprintf("question %q kind must be single_choice, multiple_choice, or text", question.ID))
		}
	}
	return nil
}

func validateNotebookAgentAnswers(
	questions []NotebookAgentQuestion,
	request AnswerNotebookAgentInteractionRequest,
) ([]NotebookAgentQuestionAnswer, *APIError) {
	if request.Declined {
		if len(request.Answers) != 0 {
			return nil, badRequestError("notebook_agent_interaction_answer_invalid", "a declined questionnaire cannot include answers")
		}
		return nil, nil
	}
	provided := make(map[string]NotebookAgentQuestionAnswer, len(request.Answers))
	for _, answer := range request.Answers {
		if _, exists := provided[answer.QuestionID]; exists {
			return nil, badRequestError("notebook_agent_interaction_answer_duplicate", fmt.Sprintf("question %q was answered more than once", answer.QuestionID))
		}
		provided[answer.QuestionID] = answer
	}
	normalized := make([]NotebookAgentQuestionAnswer, 0, len(questions))
	for _, question := range questions {
		answer, exists := provided[question.ID]
		if !exists {
			if question.Required {
				return nil, badRequestError("notebook_agent_interaction_answer_required", fmt.Sprintf("question %q requires an answer", question.ID))
			}
			continue
		}
		delete(provided, question.ID)
		switch question.Kind {
		case NotebookAgentQuestionText:
			answer.Text = strings.TrimSpace(answer.Text)
			answer.Values = nil
			if len(answer.Text) > maxNotebookAgentQuestionText || (question.Required && answer.Text == "") {
				return nil, badRequestError("notebook_agent_interaction_answer_invalid", fmt.Sprintf("question %q requires text of at most %d characters", question.ID, maxNotebookAgentQuestionText))
			}
		case NotebookAgentQuestionSingle, NotebookAgentQuestionMultiple:
			answer.Text = ""
			minimum := 0
			maximum := len(question.Options)
			if question.Required {
				minimum = 1
			}
			if question.Kind == NotebookAgentQuestionSingle {
				maximum = 1
			}
			if len(answer.Values) < minimum || len(answer.Values) > maximum {
				return nil, badRequestError("notebook_agent_interaction_answer_invalid", fmt.Sprintf("question %q has an invalid number of choices", question.ID))
			}
			allowed := make(map[string]struct{}, len(question.Options))
			for _, option := range question.Options {
				allowed[option.Value] = struct{}{}
			}
			seen := make(map[string]struct{}, len(answer.Values))
			for _, value := range answer.Values {
				if _, ok := allowed[value]; !ok {
					return nil, badRequestError("notebook_agent_interaction_answer_invalid", fmt.Sprintf("question %q contains an unknown choice", question.ID))
				}
				if _, duplicate := seen[value]; duplicate {
					return nil, badRequestError("notebook_agent_interaction_answer_invalid", fmt.Sprintf("question %q repeats a choice", question.ID))
				}
				seen[value] = struct{}{}
			}
		}
		answer.QuestionID = question.ID
		normalized = append(normalized, answer)
	}
	if len(provided) != 0 {
		return nil, badRequestError("notebook_agent_interaction_answer_unknown", "the answer contains an unknown question id")
	}
	return normalized, nil
}

func notebookAgentIdentifier(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for index := range value {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_' || character == '-' {
			if index == 0 && character >= '0' && character <= '9' {
				return false
			}
			continue
		}
		return false
	}
	return true
}

func cloneNotebookAgentQuestions(questions []NotebookAgentQuestion) []NotebookAgentQuestion {
	cloned := make([]NotebookAgentQuestion, len(questions))
	copy(cloned, questions)
	for index := range cloned {
		cloned[index].Options = append([]NotebookAgentQuestionOption(nil), questions[index].Options...)
	}
	return cloned
}
