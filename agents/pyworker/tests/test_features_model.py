import math

from pyworker.features import build_features, email_domain_type, normalise_source
from pyworker.model import FEATURES, Model, vectorize

import pytest


@pytest.fixture(scope="module")
def model():
    return Model()


def feats(**kw):
    base = {"source": "web_form", "company_size": 200, "domain_age_days": 3000,
            "email_domain_type": "corporate", "matches_open_opportunity_account": 0}
    base.update(kw)
    return base


def test_email_domain_types():
    assert email_domain_type("a@gmail.com") == "free"
    assert email_domain_type("a@Mailinator.com") == "disposable"
    assert email_domain_type("a@acme.io") == "corporate"


def test_unknown_sources_fall_back_to_other():
    assert normalise_source("Referral") == "referral" and normalise_source("tiktok") == "other" and normalise_source(None) == "other"


def test_build_features_allows_missing_enrichment():
    f = build_features({"email": "a@acme.io", "source": "event"}, None, True)
    assert f == {"source": "event", "company_size": None, "domain_age_days": None,
                 "email_domain_type": "corporate", "matches_open_opportunity_account": 1}
    f = build_features({"email": "a@acme.io"}, {"parsed": {"employees": 40, "domain_age_days": 900}}, False)
    assert f["company_size"] == 40 and f["domain_age_days"] == 900


def test_vectorize_shape_and_missing_values_are_nan():
    v = vectorize(feats(company_size=None, domain_age_days=None))
    assert len(v) == len(FEATURES)
    assert math.isnan(v[5]) and math.isnan(v[6])


def test_model_version_marks_synthetic_training(model):
    assert model.version.startswith("synthetic-v0-") and len(model.version) == len("synthetic-v0-") + 8


def test_score_is_deterministic_for_the_same_features_and_version(model):
    f = feats(source="referral", matches_open_opportunity_account=1)
    assert model.score(f) == model.score(f) == model.score(dict(f))


def test_retraining_reproduces_the_same_version_and_scores(model):
    again = Model()
    assert again.version == model.version
    for f in (feats(), feats(source="paid", email_domain_type="free"), feats(company_size=None, domain_age_days=None)):
        assert again.score(f) == model.score(f)


def test_score_range_and_reasons_shape(model):
    for f in (feats(), feats(email_domain_type="disposable"), feats(source="referral", matches_open_opportunity_account=1),
              feats(company_size=None, domain_age_days=None)):
        score, reasons = model.score(f)
        assert isinstance(score, int) and 0 <= score <= 100
        assert len(reasons) <= 3
        for r in reasons:
            assert set(r) == {"feature", "value", "impact", "direction"} and r["direction"] in ("up", "down")


def test_missing_enrichment_is_scored_not_rejected(model):
    score, _ = model.score(feats(company_size=None, domain_age_days=None))
    assert 0 <= score <= 100


def test_a_disposable_address_scores_below_a_corporate_one(model):
    # direction sanity on a deterministic model (not a quality benchmark)
    assert model.score(feats(email_domain_type="disposable"))[0] < model.score(feats())[0]
