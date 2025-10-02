"""
Unit tests for tier0 email validation.
"""
import pytest
import sys
import os

# Add orchestrator to path
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'orchestrator'))

from tier0 import (
    quick_score, is_valid_syntax, check_mx_records, 
    check_domain_reputation, analyze_local_part
)


class TestSyntaxValidation:
    """Test email syntax validation."""
    
    def test_valid_emails(self):
        """Test valid email formats."""
        valid_emails = [
            "user@example.com",
            "test.email@domain.org",
            "user+tag@example.co.uk",
            "123@numbers.com",
            "a@b.co"
        ]
        
        for email in valid_emails:
            assert is_valid_syntax(email), f"Should be valid: {email}"
    
    def test_invalid_emails(self):
        """Test invalid email formats."""
        invalid_emails = [
            "invalid",
            "@domain.com",
            "user@",
            "user..double@domain.com",
            ".user@domain.com",
            "user@domain..com",
            "user@" + "a" * 255 + ".com",  # Domain too long
            "a" * 65 + "@domain.com",      # Local part too long
        ]
        
        for email in invalid_emails:
            assert not is_valid_syntax(email), f"Should be invalid: {email}"


class TestDomainReputation:
    """Test domain reputation checks."""
    
    def test_disposable_domains(self):
        """Test disposable email detection."""
        result = check_domain_reputation("10minutemail.com")
        assert result["is_disposable"] == True
        assert result["reputation_score"] == 0.1
    
    def test_free_providers(self):
        """Test free provider detection."""
        result = check_domain_reputation("gmail.com")
        assert result["is_free_provider"] == True
        assert result["reputation_score"] == 0.6
    
    def test_business_domains(self):
        """Test business domain scoring."""
        result = check_domain_reputation("company.com")
        assert result["is_free_provider"] == False
        assert result["reputation_score"] == 0.8


class TestLocalPartAnalysis:
    """Test local part analysis."""
    
    def test_role_emails(self):
        """Test role-based email detection."""
        role_locals = ["admin", "support", "noreply", "info"]
        
        for local in role_locals:
            result = analyze_local_part(local)
            assert result["is_role"] == True
    
    def test_suspicious_patterns(self):
        """Test suspicious pattern detection."""
        suspicious_locals = ["test123", "fake.user", "spam.account"]
        
        for local in suspicious_locals:
            result = analyze_local_part(local)
            assert result["is_suspicious"] == True
    
    def test_normal_locals(self):
        """Test normal local parts."""
        normal_locals = ["john.doe", "alice", "bob123"]
        
        for local in normal_locals:
            result = analyze_local_part(local)
            assert result["is_role"] == False
            assert result["is_suspicious"] == False


class TestQuickScore:
    """Test the main quick_score function."""
    
    def test_invalid_syntax(self):
        """Test scoring for invalid syntax."""
        result = quick_score("invalid-email")
        assert result["syntax"] == False
        assert result["score"] == 0.0
        assert result["confidence"] == 0.0
    
    def test_valid_business_email(self):
        """Test scoring for valid business email."""
        result = quick_score("john.doe@company.com")
        assert result["syntax"] == True
        assert result["score"] > 0.5
        assert result["disposable"] == False
        assert result["role"] == False
    
    def test_role_email_penalty(self):
        """Test that role emails get lower scores."""
        business_result = quick_score("john@company.com")
        role_result = quick_score("admin@company.com")
        
        assert role_result["role"] == True
        assert business_result["score"] > role_result["score"]
    
    def test_disposable_email_penalty(self):
        """Test that disposable emails get very low scores."""
        result = quick_score("test@10minutemail.com")
        assert result["disposable"] == True
        assert result["score"] < 0.2
    
    def test_free_provider_scoring(self):
        """Test free provider scoring."""
        result = quick_score("user@gmail.com")
        assert result["free_provider"] == True
        assert 0.3 < result["score"] < 0.8  # Moderate score
    
    def test_detailed_analysis(self):
        """Test that detailed analysis is included."""
        result = quick_score("user@example.com")
        assert "details" in result
        assert "mx_data" in result["details"]
        assert "reputation" in result["details"]
        assert "local_analysis" in result["details"]


if __name__ == "__main__":
    pytest.main([__file__])
