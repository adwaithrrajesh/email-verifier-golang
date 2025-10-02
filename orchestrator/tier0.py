from email_validator import validate_email, EmailNotValidError
import dns.resolver
import re
from typing import Dict, List, Set

# Extended role-based email patterns
ROLE_LOCALPARTS = {
    "admin", "info", "support", "contact", "webmaster", "sales", "help",
    "noreply", "no-reply", "donotreply", "postmaster", "abuse", "security",
    "billing", "accounts", "marketing", "pr", "press", "legal", "compliance"
}

# Common disposable email domains (subset for performance)
DISPOSABLE_DOMAINS = {
    "10minutemail.com", "guerrillamail.com", "mailinator.com", "tempmail.org",
    "throwaway.email", "temp-mail.org", "yopmail.com", "maildrop.cc",
    "sharklasers.com", "guerrillamailblock.com", "pokemail.net", "spam4.me"
}

# Free email providers (affects deliverability scoring)
FREE_PROVIDERS = {
    "gmail.com", "yahoo.com", "hotmail.com", "outlook.com", "aol.com",
    "icloud.com", "protonmail.com", "mail.com", "gmx.com", "yandex.com"
}

def is_valid_syntax(email: str) -> bool:
    """Enhanced syntax validation with additional checks."""
    try:
        # Basic email-validator check
        validate_email(email, check_deliverability=False)
        
        # Additional syntax checks
        local, domain = email.rsplit("@", 1)
        
        # Check for suspicious patterns
        if ".." in email or email.startswith(".") or email.endswith("."):
            return False
            
        # Local part length check (RFC 5321)
        if len(local) > 64:
            return False
            
        # Domain length check
        if len(domain) > 253:
            return False
            
        # Check for valid characters in domain
        if not re.match(r'^[a-zA-Z0-9.-]+$', domain):
            return False
            
        return True
    except (EmailNotValidError, ValueError):
        return False

def check_mx_records(domain: str) -> Dict[str, any]:
    """Check MX records with fallback to A records."""
    result = {
        "has_mx": False,
        "mx_count": 0,
        "mx_hosts": [],
        "has_a_record": False,
        "priority_score": 0.0
    }
    
    try:
        # Try MX lookup first
        mx_answers = dns.resolver.resolve(domain, "MX")
        if mx_answers:
            result["has_mx"] = True
            result["mx_count"] = len(mx_answers)
            result["mx_hosts"] = [str(mx.exchange).rstrip('.') for mx in mx_answers]
            
            # Score based on MX setup quality
            if result["mx_count"] >= 2:
                result["priority_score"] = 1.0  # Redundant MX setup
            else:
                result["priority_score"] = 0.7  # Single MX
                
    except (dns.resolver.NXDOMAIN, dns.resolver.NoAnswer, Exception):
        pass
    
    # Fallback to A record if no MX
    if not result["has_mx"]:
        try:
            a_answers = dns.resolver.resolve(domain, "A")
            if a_answers:
                result["has_a_record"] = True
                result["priority_score"] = 0.3  # A record fallback
        except Exception:
            pass
    
    return result

def check_domain_reputation(domain: str) -> Dict[str, any]:
    """Basic domain reputation checks."""
    result = {
        "is_disposable": False,
        "is_free_provider": False,
        "is_role_domain": False,
        "reputation_score": 0.5
    }
    
    domain_lower = domain.lower()
    
    # Check disposable
    if domain_lower in DISPOSABLE_DOMAINS:
        result["is_disposable"] = True
        result["reputation_score"] = 0.1
        return result
    
    # Check free providers
    if domain_lower in FREE_PROVIDERS:
        result["is_free_provider"] = True
        result["reputation_score"] = 0.6
    
    # Check for suspicious patterns
    if any(pattern in domain_lower for pattern in ["temp", "fake", "test", "spam"]):
        result["reputation_score"] = 0.2
    
    # Business domains get higher score
    if not result["is_free_provider"] and "." in domain and len(domain.split(".")) >= 2:
        result["reputation_score"] = 0.8
    
    return result

def analyze_local_part(local: str) -> Dict[str, any]:
    """Analyze the local part of the email."""
    result = {
        "is_role": False,
        "is_suspicious": False,
        "length_score": 0.5,
        "pattern_score": 0.5
    }
    
    local_lower = local.lower()
    
    # Role-based check
    if local_lower in ROLE_LOCALPARTS:
        result["is_role"] = True
    
    # Length scoring
    if 3 <= len(local) <= 20:
        result["length_score"] = 1.0
    elif len(local) < 3 or len(local) > 30:
        result["length_score"] = 0.2
    else:
        result["length_score"] = 0.7
    
    # Pattern analysis
    if re.match(r'^[a-zA-Z0-9._-]+$', local):
        result["pattern_score"] = 1.0
    elif re.match(r'^[a-zA-Z0-9]+$', local):
        result["pattern_score"] = 0.9
    else:
        result["pattern_score"] = 0.3
    
    # Suspicious patterns
    suspicious_patterns = ["test", "fake", "spam", "noreply", "temp"]
    if any(pattern in local_lower for pattern in suspicious_patterns):
        result["is_suspicious"] = True
        result["pattern_score"] *= 0.3
    
    return result

def calculate_confidence_score(syntax: bool, mx_data: Dict, reputation: Dict, local_analysis: Dict) -> float:
    """Calculate overall confidence score using weighted factors."""
    if not syntax:
        return 0.0
    
    score = 0.0
    
    # Syntax (20%)
    score += 0.2
    
    # MX/DNS (30%)
    if mx_data["has_mx"]:
        score += 0.3 * mx_data["priority_score"]
    elif mx_data["has_a_record"]:
        score += 0.15
    
    # Domain reputation (25%)
    if reputation["is_disposable"]:
        score += 0.05  # Very low score for disposable
    else:
        score += 0.25 * reputation["reputation_score"]
    
    # Local part analysis (15%)
    if local_analysis["is_role"]:
        score += 0.05  # Lower score for role emails
    else:
        score += 0.15 * (local_analysis["length_score"] + local_analysis["pattern_score"]) / 2
    
    # Suspicious patterns penalty (10%)
    if local_analysis["is_suspicious"]:
        score += 0.02
    else:
        score += 0.1
    
    return min(1.0, max(0.0, score))

def quick_score(email: str) -> dict:
    """Enhanced email scoring with comprehensive checks."""
    result = {
        "syntax": False,
        "mx": False,
        "role": False,
        "disposable": False,
        "free_provider": False,
        "score": 0.0,
        "confidence": 0.0,
        "details": {}
    }
    
    # Syntax validation
    syntax_valid = is_valid_syntax(email)
    result["syntax"] = syntax_valid
    
    if not syntax_valid:
        return result
    
    try:
        local, domain = email.rsplit("@", 1)
        
        # MX/DNS checks
        mx_data = check_mx_records(domain)
        result["mx"] = mx_data["has_mx"] or mx_data["has_a_record"]
        
        # Domain reputation
        reputation = check_domain_reputation(domain)
        result["disposable"] = reputation["is_disposable"]
        result["free_provider"] = reputation["is_free_provider"]
        
        # Local part analysis
        local_analysis = analyze_local_part(local)
        result["role"] = local_analysis["is_role"]
        
        # Calculate scores
        confidence = calculate_confidence_score(syntax_valid, mx_data, reputation, local_analysis)
        result["confidence"] = confidence
        result["score"] = confidence  # Legacy compatibility
        
        # Store detailed analysis
        result["details"] = {
            "mx_data": mx_data,
            "reputation": reputation,
            "local_analysis": local_analysis
        }
        
    except Exception as e:
        result["score"] = 0.1
        result["confidence"] = 0.1
        result["details"] = {"error": str(e)}
    
    return result
