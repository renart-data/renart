package service

import (
	"encoding/json"
	"reflect"

	"renart/internal/web/notebook"
	"renart/internal/web/presentation"
)

// notebookParameterDefinitionKey identifies the authored parameter contract
// without coupling runtime values to the notebook's wider content revision.
// Any definition change resets local overrides to the new defaults.
func notebookParameterDefinitionKey(nb *notebook.Notebook) string {
	if nb == nil {
		return "[]"
	}
	encoded, err := json.Marshal(nb.Parameters)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func cloneNotebookParameterValues(values map[string]any) map[string]any {
	if values == nil {
		return map[string]any{}
	}
	result := make(map[string]any, len(values))
	for key, value := range values {
		result[key] = cloneJSONValue(value)
	}
	return result
}

func notebookParameterDefaults(nb *notebook.Notebook) map[string]any {
	if nb == nil {
		return map[string]any{}
	}
	values, _ := presentation.ResolveParameterValues(nb.Parameters, nil)
	return values
}

// ensureNotebookParameterRuntime initializes the runtime value snapshot and
// reconciles it after an authored definition change. It returns true only when
// an already-initialized contract changed and therefore invalidated results.
func ensureNotebookParameterRuntime(nb *notebook.Notebook, rt *notebookRuntime) bool {
	if nb == nil || rt == nil {
		return false
	}
	definitionKey := notebookParameterDefinitionKey(nb)
	defaults := notebookParameterDefaults(nb)

	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.parameterDefinitionKey == "" {
		rt.parameterDefinitionKey = definitionKey
		rt.parameterValues = defaults
		return false
	}
	if rt.parameterDefinitionKey == definitionKey {
		if rt.parameterValues == nil {
			rt.parameterValues = defaults
		}
		return false
	}

	rt.parameterDefinitionKey = definitionKey
	rt.parameterValues = defaults
	for _, cell := range nb.Cells {
		rt.stale[cell.ID] = true
		delete(rt.autoFailed, cell.ID)
	}
	return true
}

func (s *NotebookService) currentNotebookParameterValues(nb *notebook.Notebook) map[string]any {
	s.hydrateRuntime(nb)
	rt := s.runtimes.get(nb.UUID)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return cloneNotebookParameterValues(rt.parameterValues)
}

// updateNotebookParameterValues applies a partial local override on top of the
// current runtime snapshot. Runtime values never rewrite notebook.yml. A value
// change invalidates every data-producing cell conservatively because Jinja,
// Python, and source templates may all consume parameters.
func (s *NotebookService) updateNotebookParameterValues(
	notebookID string,
	nb *notebook.Notebook,
	overrides map[string]any,
	schedule bool,
) (map[string]any, *APIError) {
	s.hydrateRuntime(nb)
	rt := s.runtimes.get(nb.UUID)
	rt.mu.Lock()
	merged := cloneNotebookParameterValues(rt.parameterValues)
	rt.mu.Unlock()
	for id, value := range overrides {
		merged[id] = cloneJSONValue(value)
	}

	resolved, findings := presentation.ResolveParameterValues(nb.Parameters, merged)
	if len(findings) > 0 {
		return nil, &APIError{Status: 400, Code: "invalid_notebook_parameter_values", Message: findings[0].Message}
	}

	rt.mu.Lock()
	if reflect.DeepEqual(rt.parameterValues, resolved) {
		values := cloneNotebookParameterValues(rt.parameterValues)
		rt.mu.Unlock()
		return values, nil
	}
	rt.parameterValues = cloneNotebookParameterValues(resolved)
	for _, cell := range nb.Cells {
		rt.stale[cell.ID] = true
		rt.authoredFingerprints[cell.ID] = notebook.CellFingerprintWithParameters(nb, cell, resolved)
		delete(rt.autoFailed, cell.ID)
	}
	autoRun := rt.autoRun
	auto := rt.autoRecompute
	values := cloneNotebookParameterValues(rt.parameterValues)
	rt.mu.Unlock()

	if autoRun != nil {
		autoRun.cancel()
	}
	s.publishRuntime(notebookID, nb.UUID, nil, nil)
	if schedule && auto {
		s.scheduleRecompute(notebookID, nb.UUID)
	}
	return values, nil
}

func (s *NotebookService) onNotebookParametersChanged(notebookID string, nb *notebook.Notebook) {
	s.hydrateRuntime(nb)
	rt := s.runtimes.get(nb.UUID)
	rt.mu.Lock()
	autoRun := rt.autoRun
	auto := rt.autoRecompute
	rt.mu.Unlock()
	if autoRun != nil {
		autoRun.cancel()
	}
	s.publishRuntime(notebookID, nb.UUID, nil, nil)
	if auto {
		s.scheduleRecompute(notebookID, nb.UUID)
	}
}
