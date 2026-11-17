"""Feature extraction. Reproducible: score = f(features, model_version)."""
from typing import Optional

SOURCES = ["web_form", "referral", "event", "paid", "other"]

# Keep in sync with internal/lead/emailtype.go.
FREE = {"gmail.com", "googlemail.com", "yahoo.com", "outlook.com", "hotmail.com", "live.com", "msn.com",
        "icloud.com", "me.com", "aol.com", "proton.me", "protonmail.com", "gmx.com", "mail.com",
        "yandex.com", "zoho.com"}
DISPOSABLE = {"mailinator.com", "guerrillamail.com", "10minutemail.com", "tempmail.com", "yopmail.com",
              "trashmail.com", "sharklasers.com", "getnada.com"}


def email_domain_type(email: str) -> str:
    d = email.rsplit("@", 1)[-1].lower() if "@" in email else ""
    if d in DISPOSABLE:
        return "disposable"
    if d in FREE:
        return "free"
    return "corporate"


def normalise_source(source: Optional[str]) -> str:
    s = (source or "").lower()
    return s if s in SOURCES else "other"


def build_features(lead: dict, enrichment: Optional[dict], matches_open_opportunity_account: bool) -> dict:
    """Raw, JSON-serialisable features (None = unknown). Stored with the score."""
    parsed = (enrichment or {}).get("parsed", {}) or {}
    return {
        "source": normalise_source(lead.get("source")),
        "company_size": parsed.get("employees"),
        "domain_age_days": parsed.get("domain_age_days"),
        "email_domain_type": email_domain_type(lead.get("email", "")),
        "matches_open_opportunity_account": 1 if matches_open_opportunity_account else 0,
    }
