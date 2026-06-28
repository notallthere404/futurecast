package config

// Inference `inference:` block.
//
// YAML shape:
//
//	inference:
//	  mode: continuous
//	  type: llm
//	  model: Qwen/Qwen3-Reranker-8B
//	  cutoff: 0.0           # global default
//	  top_n: 3              # global default
//
//	  events:               # classification name (dynamic key)
//	    - name: vector
//	      prompt: "{label}: {definition}"
//	      labels: [...]
//	    - name: sector
//	      cutoff: 0.1       # overrides global
//	      top_n: 5
//	      labels: [...]
//
// Cascade: per-attribute Cutoff/TopN override the global defaults; nil
// means inherit. Classification names are dynamic top-level keys captured
// via mapstructure `",remain"` into the Classifications map.
type Inference struct {
	Addr    string            `mapstructure:"addr"`
	Mode    InferenceMode     `mapstructure:"mode"`   // continuous | manual
	Type    InferenceType     `mapstructure:"type"`   // zeroshot | llm | api
	Engine  InferenceEngine   `mapstructure:"engine"` // self-hosted runtime; ignored when Type=api
	Model   string            `mapstructure:"model"`
	Default InferenceDefaults `mapstructure:"default"` // cutoff + top_n inherited by attributes

	// Api consulted only when Type=api.
	Api *InferenceAPI `mapstructure:"api"`

	// Classifications dynamic top-level keys (events, actors, …).
	// Value = ordered list of attributes belonging to that classification.
	// Iteration order across classifications is map-non-deterministic; UI
	// should sort or use a per-classification order field if needed.
	Classifications map[string][]InferenceAttribute `mapstructure:",remain"`
}

// InferenceMode when inference runs.
type InferenceMode string

const (
	InfModeContinuous InferenceMode = "continuous" // background loop
	InfModeManual     InferenceMode = "manual"     // explicit trigger only
)

// InferenceType what shape of model the pipeline runs against.
type InferenceType string

const (
	InfTypeZeroshot InferenceType = "zeroshot" // self-hosted zero-shot model
	InfTypeLlm      InferenceType = "llm"      // self-hosted LLM
	InfTypeAPI      InferenceType = "api"      // remote API
)

// InferenceEngine names the runtime that hosts the model when Type
// is self-hosted (zeroshot or llm). Ignored when Type=api.
//
// Only "transformers" is supported today; the type exists so adding
// llama.cpp or vllm in the future is a single new constant + a Python
// service Dockerfile, not a config schema change.
type InferenceEngine string

const (
	EngineTransformers InferenceEngine = "transformers" // HF transformers + torch + fastapi
	// EngineLlamaCpp  InferenceEngine = "llama.cpp"    // future
	// EngineVllm      InferenceEngine = "vllm"         // future.
)

// InferenceDefaults holds the fields under `inference.default:`
// inherited by every attribute unless the attribute overrides them.
type InferenceDefaults struct {
	Cutoff float64 `mapstructure:"cutoff"`
	TopN   int     `mapstructure:"top_n"`
}

// InferenceAPI endpoint config for Type=api.
type InferenceAPI struct {
	Endpoint      string `mapstructure:"endpoint"`
	APIKey        string `mapstructure:"api_key"`
	ModelOverride string `mapstructure:"model_override"` // empty = use Model from root
}

// InferenceAttribute one attribute inside a classification.
// Cutoff/TopN are pointers so nil distinguishes "not set" (inherit) from
// "explicitly zero" (override to zero).
type InferenceAttribute struct {
	Name        string           `mapstructure:"name"`
	Prompt      string           `mapstructure:"prompt"`
	Instruction string           `mapstructure:"instruction"`
	Labels      []InferenceLabel `mapstructure:"labels"`
	Cutoff      *float64         `mapstructure:"cutoff"`
	TopN        *int             `mapstructure:"top_n"`
}

// InferenceLabel label + definition pair. Bound table so the two
// can't drift; old config used parallel arrays (anti-pattern).
//
// To support a flat string shortcut (`labels: ["A", "B"]` → Definition=""),
// register a mapstructure DecodeHook at Load() time. Default form is the
// paired table.
type InferenceLabel struct {
	Name       string `mapstructure:"name"`
	Definition string `mapstructure:"definition"`
}

// EffectiveCutoff resolves the attribute's cutoff against the global default.
func (a InferenceAttribute) EffectiveCutoff(global float64) float64 {
	if a.Cutoff != nil {
		return *a.Cutoff
	}
	return global
}

// EffectiveTopN resolves the attribute's top_n against the global default.
func (a InferenceAttribute) EffectiveTopN(global int) int {
	if a.TopN != nil {
		return *a.TopN
	}
	return global
}
