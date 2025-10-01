from email_validator import validate_email, EmailNotValidError
import dns.resolver

ROLE_LOCALPARTS = {"admin","info","support","contact","webmaster","sales","help"}

def quick_score(email: str) -> dict:
    try:
        validate_email(email, check_deliverability=False)
        syntax = True
    except EmailNotValidError:
        return {"syntax": False, "mx": False, "role": False, "score": 0.0}

    domain = email.split("@", 1)[1].lower()
    has_mx = False
    try:
        answers = dns.resolver.resolve(domain, "MX")
        has_mx = len(answers) > 0
    except Exception:
        has_mx = False

    local = email.split("@", 1)[0].lower()
    role = local in ROLE_LOCALPARTS

    score = 0.0
    score += 0.6 if syntax else 0
    score += 0.3 if has_mx else 0
    score -= 0.1 if role else 0
    score = max(0.0, min(1.0, score))
    return {"syntax": syntax, "mx": has_mx, "role": role, "score": score}
