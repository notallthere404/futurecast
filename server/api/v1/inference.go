package v1

//
// Sent from the server to the Python inference service. The inference
// service is stateless w.r.t. classifications; every request carries
// the full spec (which classifications, which attributes, which labels,
// which prompt). The server holds the source of truth.

// ClassifyRequest is the payload posted to the inference service per article.
type ClassifyRequest struct {
	ID              string               `json:"id"`
	Content         string               `json:"content"`
	Timestamp       string               `json:"timestamp"` // RFC3339
	Classifications []ClassificationSpec `json:"classifications"`
}

// ClassificationSpec one classification group (e.g. "events") with
// its set of attributes to run.
type ClassificationSpec struct {
	Name       string          `json:"name"`
	Attributes []AttributeSpec `json:"attributes"`
}

// AttributeSpec one pass through the loaded classifier. Same wire
// shape regardless of model type; the Python side dispatches on its
// loaded classifier (zero-shot vs LLM) and reads only the fields it
// needs (zero-shot uses Name + Prompt; LLM also uses Definition +
// Instruction).
type AttributeSpec struct {
	Name        string      `json:"name"`
	Labels      []LabelSpec `json:"labels"`
	Prompt      string      `json:"prompt"`
	Instruction string      `json:"instruction,omitempty"`
	TopN        int         `json:"top_n"`
	Cutoff      float64     `json:"cutoff"`
}

// LabelSpec label name + optional definition. Definition stays empty
// for zero-shot mode (Python uses Name only); LLM mode substitutes both
// into the prompt template.
type LabelSpec struct {
	Name       string `json:"name"`
	Definition string `json:"definition,omitempty"`
}

// InferenceInfo is the response body of `GET /info` on the inference
// service. The Go-side readiness goroutine compares Type + Model
// against the live config; mismatch triggers POST /load.
type InferenceInfo struct {
	Type  string `json:"type"`  // zeroshot | llm
	Model string `json:"model"` // currently loaded model id
	State string `json:"state"` // init | live | error
}

// InferenceLoad is the request body of `POST /load`. The Python service
// responds 202 immediately and swaps the loaded classifier in a
// background task; readiness flips back to `live` once swap completes.
type InferenceLoad struct {
	Type  string `json:"type"`
	Model string `json:"model"`
}
