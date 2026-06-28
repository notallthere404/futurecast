from __future__ import annotations

from uuid import uuid4

from transformers import pipeline

from models import (
    Attribute,
    ClassifyRequest,
    ClassifyResponseItem,
    LabelScore,
)


class Classifier:
    """Wraps a single transformers zero-shot pipeline. The per-request
    spec (classifications, attributes, labels, prompts) is carried
    inline by ClassifyRequest; no config is held on the classifier.
    """

    type: str = "zeroshot"

    def __init__(self, model: str):
        self.model = model
        self.pipeline = pipeline("zero-shot-classification", model=model)

    def classify(self, req: ClassifyRequest) -> list[ClassifyResponseItem]:
        results: list[ClassifyResponseItem] = []
        for cls in req.classifications:
            data: dict[str, list[LabelScore]] = {}
            for attr in cls.attributes:
                data[attr.name] = self._run_attribute(req.content, attr)
            results.append(ClassifyResponseItem(
                classification=cls.name,
                id=uuid4(),
                article_id=req.id,
                timestamp=req.timestamp,
                data=data,
            ))
        return results

    def _run_attribute(self, content: str, attr: Attribute) -> list[LabelScore]:
        # multi_label=True returns independent scores per label; required
        # when the caller wants top_n > 1 with possibly non-exclusive labels.
        multi_label = attr.top_n > 1

        # Zero-shot only reads label names; definitions are LLM-only.
        label_names = [l.name for l in attr.labels]

        raw = self.pipeline(
            content,
            label_names,
            hypothesis_template=attr.prompt,
            multi_label=multi_label,
        )

        scored = [
            LabelScore(label=label, score=score)
            for label, score in zip(raw["labels"], raw["scores"])
        ]

        # Drop anything below the per-attribute cutoff; if everything is
        # dropped, return a single sentinel "n/i" entry so the downstream
        # shape stays consistent.
        filtered = [s for s in scored if s.score >= attr.cutoff]
        if not filtered:
            filtered = [LabelScore(label="n/i", score=0.0)]

        return filtered[: attr.top_n]
