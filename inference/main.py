from __future__ import annotations

import asyncio
import logging
import os
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


DEFAULT_MODEL = "MoritzLaurer/deberta-v3-base-zeroshot-v2.0"
DEFAULT_TYPE = "zeroshot"

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
    """Load the model and mark live. Classification spec arrives
    per-request, so nothing else is held on app state. Model id + type
    come from env at boot; POST /load swaps them at runtime."""
    app.state.health_status = "init"
    app.state.deps = None
    app.state.current_type = os.getenv("INFERENCE_TYPE", DEFAULT_TYPE)
    app.state.current_model = os.getenv("INFERENCE_MODEL", DEFAULT_MODEL)
    app.state.load_lock = asyncio.Lock()

    try:
        log.info(
            "loading model type=%s model=%s",
            app.state.current_type, app.state.current_model,
        )
        # Off the event loop so uvicorn keeps answering /health while
        # the model loads. Without this the socket accepts connections
        # but reads block until load completes, looking like a hang to
        # any caller (including the readiness-gate goroutine on the Go
        # side).
        classifier = await asyncio.to_thread(
            _build_classifier,
            app.state.current_type, app.state.current_model,
        )
        app.state.deps = AppState(classifier=classifier)
        app.state.health_status = "live"
        log.info("listening")
    except Exception as e:
        app.state.health_status = "error"
        log.error("startup failed error=%s", e)

    yield
    app.state.deps = None


app = FastAPI(lifespan=lifespan)


@app.get("/health", response_model=HealthResponse)
def health(request: Request):
    status = getattr(request.app.state, "health_status", "init")
    body = HealthResponse(state=status, time=datetime.now().strftime("%H:%M:%S"))
    if status != "live":
        return JSONResponse(status_code=503, content=body.model_dump())
    return body


@app.get("/info", response_model=InfoResponse)
def info(request: Request):
    s = request.app.state
    return InfoResponse(
        type=getattr(s, "current_type", DEFAULT_TYPE),
        model=getattr(s, "current_model", DEFAULT_MODEL),
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
