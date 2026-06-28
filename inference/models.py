from pydantic import BaseModel, Field
from typing import Annotated, Literal
from uuid import UUID
from annotated_types import MinLen


# ── Per-request classification spec ─────────────────────────────────────────
# The server owns the source of truth for what to classify against; the
# inference service is stateless. Each request includes the attributes,
# labels, and prompts to run.

class Label(BaseModel):
    """Label name + optional definition. Definition is only meaningful
    in LLM mode; zero-shot classifiers ignore it."""
    name: str
    definition: str = ""


class Attribute(BaseModel):
    """A single classify pass. The server sends the same wire shape
    regardless of classifier type; each classifier reads only the
    fields it needs (zero-shot uses name + prompt; LLM also reads
    definition + instruction)."""
    name: str
    labels: Annotated[list[Label], MinLen(1)]
    prompt: str = Field(default="The topic of this text is {}")
    instruction: str = ""
    top_n: int = Field(default=1, ge=1)
    cutoff: float = Field(default=0.0, ge=0.0, le=1.0)


class Classification(BaseModel):
    """Group of attributes under one classification name (e.g. "events")."""
    name: str
    attributes: Annotated[list[Attribute], MinLen(1)]


class ClassifyRequest(BaseModel):
    id: UUID
    content: str
    timestamp: str
    classifications: Annotated[list[Classification], MinLen(1)]


# ── Response ────────────────────────────────────────────────────────────────

class LabelScore(BaseModel):
    label: str
    score: float


class ClassifyResponseItem(BaseModel):
    """One response per classification in the request. The Go side
    iterates these and stores each under its classification name."""
    classification: str
    id: UUID
    article_id: UUID
    timestamp: str
    data: dict[str, list[LabelScore]]  # attribute name → scored labels


# ── Health / info / load ────────────────────────────────────────────────────

class HealthResponse(BaseModel):
    state: Literal["init", "live", "error"]
    time: str


class InfoResponse(BaseModel):
    """GET /info — what the service currently has loaded. The Go-side
    readiness goroutine compares this against the live config; a
    mismatch fires POST /load."""
    type: Literal["zeroshot", "llm"]
    model: str
    state: Literal["init", "live", "error"]


class LoadRequest(BaseModel):
    """POST /load — swap the loaded classifier. Service responds 202
    immediately and performs the swap in a background task; readiness
    flips back to `live` once the new model is loaded."""
    type: Literal["zeroshot", "llm"]
    model: str
