# syntax=docker/dockerfile:1.7

# ────────────────────────── build stage ──────────────────────────
# Install every Python dep into an isolated venv at /opt/venv. The
# runtime stage copies the venv whole, leaving build tooling behind.
FROM python:3.11-slim-bookworm AS build

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_NO_CACHE_DIR=1

WORKDIR /build
RUN python -m venv /opt/venv
ENV PATH="/opt/venv/bin:$PATH"

# torch lands first from the CPU-only index so the much larger CUDA
# wheel never gets pulled. Order matters: subsequent pip install -r
# sees torch is satisfied and won't re-resolve it from PyPI.
RUN pip install --upgrade pip && \
    pip install torch --index-url https://download.pytorch.org/whl/cpu

COPY requirements.txt .
RUN pip install -r requirements.txt

# ────────────────────────── runtime stage ──────────────────────────
FROM python:3.11-slim-bookworm AS runtime

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PATH="/opt/venv/bin:$PATH" \
    HF_HOME=/home/app/.cache/huggingface

# ca-certificates only; nothing else from apt is needed at runtime.
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# Unprivileged user with a real home directory so HF cache writes land
# somewhere the user owns.
RUN groupadd --system app && \
    useradd --system --gid app --create-home --shell /usr/sbin/nologin app

COPY --from=build /opt/venv /opt/venv

WORKDIR /app
COPY --chown=app:app classifier.py main.py models.py ./

USER app

# Pre-create the cache dir as the app user so a fresh named volume
# inherits app:app ownership at first mount. Without this Docker
# creates the mount point as root:root and the non-root process gets
# PermissionError on the first model download.
RUN mkdir -p /home/app/.cache/huggingface

# Model cache volume mount target. Persists pulled weights across
# container restarts so cold start only pays the download once.
VOLUME ["/home/app/.cache/huggingface"]

EXPOSE 8080

# Long start-period because first-run model download can take minutes.
# Once cached, /health flips to live within seconds; the check uses
# stdlib so no curl install is needed.
HEALTHCHECK --interval=15s --timeout=5s --start-period=600s --retries=3 \
    CMD python -c "import os,sys,urllib.request as u; r=u.urlopen('http://localhost:'+os.getenv('INFERENCE_PORT','8080')+'/health', timeout=4); sys.exit(0 if r.status==200 else 1)" || exit 1

CMD ["sh", "-c", "uvicorn main:app --host 0.0.0.0 --port ${INFERENCE_PORT:-8080}"]
