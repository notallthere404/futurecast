from __future__ import annotations

import asyncio
import logging
from contextlib import asynccontextmanager
from dataclasses import dataclass
from datetime import datetime

from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import JSONResponse

from classifier import Classifier
from models import (
    ClassifyRequest,
    ClassifyResponseItem,
    HealthResponse,
    InfoResponse,
    LoadRequest,
)

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s level=%(levelname)s msg=%(message)s",
)
log = logging.getLogger("inference")


@dataclass
class AppState:
    classifier: Classifier


def _build_classifier(classifier_type: str, model: str) -> Classifier:
    """Construct a Classifier for the requested type. Only zeroshot is
    wired today; llm raises so the Go side surfaces a clear error
    instead of silently downgrading."""
    if classifier_type == "zeroshot":
        return Classifier(model)
    raise ValueError(f"classifier type {classifier_type!r} not implemented")


@asynccontextmanager
async def lifespan(app: FastAPI):
    """Boot in `idle`: no model loaded, no env-driven defaults. The Go
    server reads config.yml and pushes the model via POST /load. Single
    source of truth, no cold-boot dual load."""
    app.state.health_status = "idle"
    app.state.deps = None
    app.state.current_type = ""
    app.state.current_model = ""
    app.state.load_lock = asyncio.Lock()
    log.info("listening (idle, awaiting /load)")
    yield
    app.state.deps = None


app = FastAPI(lifespan=lifespan)


@app.get("/health", response_model=HealthResponse)
def health(request: Request):
    status = getattr(request.app.state, "health_status", "init")
    body = HealthResponse(state=status, time=datetime.now().strftime("%H:%M:%S"))
    # idle and live are both "reachable"; init (mid-swap) and error are not.
    if status not in ("idle", "live"):
        return JSONResponse(status_code=503, content=body.model_dump())
    return body


@app.get("/info", response_model=InfoResponse)
def info(request: Request):
    s = request.app.state
    return InfoResponse(
        type=getattr(s, "current_type", ""),
        model=getattr(s, "current_model", ""),
        state=getattr(s, "health_status", "init"),
    )


@app.post("/load", status_code=202)
async def load(request: Request, body: LoadRequest):
    """Swap the loaded classifier. Returns 202 immediately and runs
    the swap in a background task so the caller does not block on
    model loading. The Go side polls /health afterwards and waits for
    state=live before resuming classify calls."""
    asyncio.create_task(_do_load(request.app, body))
    return {"accepted": True, "type": body.type, "model": body.model}


async def _do_load(app: FastAPI, body: LoadRequest):
    async with app.state.load_lock:
        log.info("load requested type=%s model=%s", body.type, body.model)
        app.state.health_status = "init"
        try:
            # Run the synchronous HF download + load off the event loop so
            # /health stays responsive while the swap runs.
            new_classifier = await asyncio.to_thread(
                _build_classifier, body.type, body.model,
            )
            app.state.deps = AppState(classifier=new_classifier)
            app.state.current_type = body.type
            app.state.current_model = body.model
            app.state.health_status = "live"
            log.info(
                "model swapped type=%s model=%s",
                body.type, body.model,
            )
        except Exception as e:
            app.state.health_status = "error"
            log.error("load failed type=%s model=%s error=%s",
                      body.type, body.model, e)


def get_deps(request: Request) -> AppState:
    deps: AppState | None = getattr(request.app.state, "deps", None)
    if deps is None:
        raise HTTPException(status_code=503, detail="model not loaded")
    return deps


@app.post("/classify", response_model=list[ClassifyResponseItem])
async def classify(request: Request, item: ClassifyRequest):
    # Run on a worker thread so the event loop stays free to serve
    # /health and /info during long classify calls. Without this a
    # single in-flight classify blocks every other request.
    deps = get_deps(request)
    try:
        return await asyncio.to_thread(deps.classifier.classify, item)
    except Exception as err:
        log.error("classify failed error=%s", err)
        raise HTTPException(status_code=500, detail=str(err))


@app.post("/classify/batch", response_model=list[list[ClassifyResponseItem]])
async def classify_batch(request: Request, items: list[ClassifyRequest]):
    deps = get_deps(request)
    try:
        return await asyncio.to_thread(
            lambda: [deps.classifier.classify(req) for req in items]
        )
    except Exception as err:
        log.error("classify batch failed error=%s", err)
        raise HTTPException(status_code=500, detail=str(err))
