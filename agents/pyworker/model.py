"""Lead scoring v0: XGBoost trained on a documented SYNTHETIC dataset.

There is no pilot history yet, so every score from this model carries a
`synthetic-` model_version and the UI labels it "preliminary". Scores are
deterministic: same (features, model_version) -> same score and reasons.
"""
import hashlib
import json
import math
from typing import Dict, List, Tuple

import numpy as np
import xgboost as xgb

from .features import SOURCES

NAN = float("nan")
EMAIL_TYPES = ["corporate", "free", "disposable"]
FEATURES = [f"source={s}" for s in SOURCES] + [
    "company_size_log", "domain_age_years", "email_type", "matches_open_opportunity_account"]

# Dataset generator spec: part of the model version, so changing it changes the version.
SPEC = {"generator": "v0", "n": 4000, "seed": 7,
        "sources": dict(zip(SOURCES, [0.45, 0.2, 0.1, 0.15, 0.1])),
        "p_missing_size": 0.25, "p_missing_age": 0.2}
PARAMS = {"objective": "binary:logistic", "max_depth": 3, "eta": 0.2, "seed": 0, "nthread": 1,
          "tree_method": "hist", "subsample": 1.0, "base_score": 0.5, "eval_metric": "logloss"}
ROUNDS = 60
SRC_EFFECT = {"web_form": 0.0, "referral": 1.1, "event": 0.5, "paid": -0.3, "other": -0.2}


def vectorize(f: dict) -> List[float]:
    row = [1.0 if f.get("source") == s else 0.0 for s in SOURCES]
    size = f.get("company_size")
    row.append(math.log1p(size) if size else NAN)
    age = f.get("domain_age_days")
    row.append(min(age, 365 * 30) / 365.0 if age is not None else NAN)
    row.append(float(EMAIL_TYPES.index(f.get("email_domain_type", "corporate"))))
    row.append(float(f.get("matches_open_opportunity_account", 0)))
    return row


def synthetic_dataset() -> Tuple[np.ndarray, np.ndarray]:
    rng = np.random.default_rng(SPEC["seed"])
    n = SPEC["n"]
    src = rng.choice(len(SOURCES), size=n, p=list(SPEC["sources"].values()))
    size = np.exp(rng.normal(3.5, 1.4, n))
    age = rng.exponential(6.0, n)
    etype = rng.choice(3, size=n, p=[0.6, 0.35, 0.05])
    match = (rng.random(n) < 0.08).astype(float)
    logit = (-1.4 + np.array([SRC_EFFECT[SOURCES[i]] for i in src]) + 0.35 * np.log1p(size) - 1.0
             + 0.08 * np.minimum(age, 15) - 0.9 * (etype == 1) - 2.5 * (etype == 2) + 1.8 * match
             + rng.normal(0, 0.7, n))
    y = (rng.random(n) < 1 / (1 + np.exp(-logit))).astype(int)
    X = np.zeros((n, len(FEATURES)))
    X[np.arange(n), src] = 1.0
    k = len(SOURCES)
    X[:, k] = np.where(rng.random(n) < SPEC["p_missing_size"], NAN, np.log1p(size))
    X[:, k + 1] = np.where(rng.random(n) < SPEC["p_missing_age"], NAN, np.minimum(age, 30) / 1.0)
    X[:, k + 2] = etype
    X[:, k + 3] = match
    return X, y


class Model:
    def __init__(self):
        X, y = synthetic_dataset()
        self.booster = xgb.train(PARAMS, xgb.DMatrix(X, label=y, feature_names=FEATURES), num_boost_round=ROUNDS)
        digest = hashlib.sha256(json.dumps([SPEC, PARAMS, ROUNDS], sort_keys=True).encode()).hexdigest()[:8]
        self.version = f"synthetic-v0-{digest}"

    def score(self, features: dict) -> Tuple[int, List[dict]]:
        x = np.array([vectorize(features)], dtype=float)
        d = xgb.DMatrix(x, feature_names=FEATURES)
        prob = float(self.booster.predict(d)[0])
        contribs = self.booster.predict(d, pred_contribs=True)[0][:-1]  # last column = bias
        k = len(SOURCES)
        grouped = {"source": float(sum(contribs[:k]))}
        for i, name in enumerate(FEATURES[k:], start=k):
            grouped[name.replace("_log", "").replace("_years", "")] = float(contribs[i])
        raw = {"source": features.get("source"), "company_size": features.get("company_size"),
               "domain_age": features.get("domain_age_days"), "email_type": features.get("email_domain_type"),
               "matches_open_opportunity_account": features.get("matches_open_opportunity_account")}
        reasons = []
        for name, impact in sorted(grouped.items(), key=lambda kv: -abs(kv[1]))[:3]:
            if abs(impact) < 0.05:
                continue
            reasons.append({"feature": name, "value": raw.get(name), "impact": round(impact, 4),
                            "direction": "up" if impact > 0 else "down"})
        return max(0, min(100, int(round(prob * 100)))), reasons
