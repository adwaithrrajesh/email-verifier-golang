"""
Integration tests for the email verification system.
Tests the full workflow using MailHog for safe SMTP testing.
"""
import pytest
import requests
import time
import json
from typing import List, Dict


class TestEmailVerificationIntegration:
    """Integration tests for the complete email verification workflow."""
    
    BASE_URL = "http://localhost:8080"
    MAILHOG_URL = "http://localhost:8025"
    
    @pytest.fixture(autouse=True)
    def setup_method(self):
        """Setup method run before each test."""
        # Wait for services to be ready
        self.wait_for_service(self.BASE_URL + "/health", timeout=30)
        self.wait_for_service(self.MAILHOG_URL + "/api/v1/messages", timeout=30)
    
    def wait_for_service(self, url: str, timeout: int = 30):
        """Wait for a service to become available."""
        start_time = time.time()
        while time.time() - start_time < timeout:
            try:
                response = requests.get(url, timeout=5)
                if response.status_code < 500:
                    return
            except requests.RequestException:
                pass
            time.sleep(1)
        
        pytest.skip(f"Service not available at {url}")
    
    def test_health_check(self):
        """Test that the orchestrator health check works."""
        response = requests.get(f"{self.BASE_URL}/health")
        assert response.status_code == 200
        
        data = response.json()
        assert data["status"] == "healthy"
        assert data["redis"] == "connected"
    
    def test_metrics_endpoint(self):
        """Test that metrics endpoint works."""
        response = requests.get(f"{self.BASE_URL}/metrics")
        assert response.status_code == 200
        
        data = response.json()
        assert "tasks_pending" in data
        assert "results_total" in data
        assert "status" in data
    
    def test_single_email_verification(self):
        """Test verification of a single email."""
        test_email = "test@example.com"
        
        # Start verification
        payload = {
            "emails": [test_email],
            "sender": "noreply@mail-verifier.local"
        }
        
        response = requests.post(f"{self.BASE_URL}/verify/start", json=payload)
        assert response.status_code == 200
        
        data = response.json()
        job_id = data["job_id"]
        assert data["queued"] == 1
        assert data["tier0_ready"] == True
        
        # Get tier0 results
        tier0_response = requests.get(f"{self.BASE_URL}/verify/{job_id}/tier0")
        assert tier0_response.status_code == 200
        
        tier0_data = tier0_response.json()
        assert test_email in tier0_data
        assert "score" in tier0_data[test_email]
        
        # Poll for final results
        results = self.poll_for_results(job_id, expected_count=1, timeout=30)
        assert len(results) == 1
        
        result = results[0]
        assert result["email"] == test_email
        assert result["status"] in ["valid", "invalid", "unknown"]
        assert "confidence" in result
        assert "attempts" in result
    
    def test_batch_email_verification(self):
        """Test verification of multiple emails."""
        test_emails = [
            "user1@example.com",
            "user2@example.com", 
            "admin@example.com",
            "test@disposable.com"
        ]
        
        payload = {
            "emails": test_emails,
            "sender": "noreply@mail-verifier.local"
        }
        
        response = requests.post(f"{self.BASE_URL}/verify/start", json=payload)
        assert response.status_code == 200
        
        data = response.json()
        job_id = data["job_id"]
        assert data["queued"] == len(test_emails)
        
        # Poll for all results
        results = self.poll_for_results(job_id, expected_count=len(test_emails), timeout=60)
        assert len(results) == len(test_emails)
        
        # Verify all emails were processed
        processed_emails = {r["email"] for r in results}
        assert processed_emails == set(test_emails)
        
        # Check that results have required fields
        for result in results:
            assert "email" in result
            assert "status" in result
            assert "confidence" in result
            assert "message" in result
            assert result["status"] in ["valid", "invalid", "unknown"]
    
    def test_mailhog_smtp_verification(self):
        """Test SMTP verification using MailHog."""
        # MailHog accepts all emails, so we can test the SMTP flow
        test_email = "testuser@localhost"
        
        payload = {
            "emails": [test_email],
            "sender": "noreply@mail-verifier.local"
        }
        
        response = requests.post(f"{self.BASE_URL}/verify/start", json=payload)
        assert response.status_code == 200
        
        job_id = response.json()["job_id"]
        
        # Poll for results
        results = self.poll_for_results(job_id, expected_count=1, timeout=30)
        assert len(results) == 1
        
        result = results[0]
        # MailHog should accept the email (or fail due to localhost domain)
        assert result["status"] in ["valid", "invalid", "unknown"]
        
        # Check that SMTP was attempted (should have MX info)
        assert "mx" in result
    
    def test_invalid_email_handling(self):
        """Test handling of invalid emails."""
        invalid_emails = [
            "invalid-email",
            "@domain.com",
            "user@",
            "user@nonexistent-domain-12345.com"
        ]
        
        payload = {
            "emails": invalid_emails,
            "sender": "noreply@mail-verifier.local"
        }
        
        response = requests.post(f"{self.BASE_URL}/verify/start", json=payload)
        assert response.status_code == 200
        
        job_id = response.json()["job_id"]
        
        # Get tier0 results - should catch syntax errors
        tier0_response = requests.get(f"{self.BASE_URL}/verify/{job_id}/tier0")
        assert tier0_response.status_code == 200
        
        tier0_data = tier0_response.json()
        
        # Check that invalid syntax emails get low scores
        for email in invalid_emails[:3]:  # First 3 are syntax errors
            if email in tier0_data:
                assert tier0_data[email]["score"] == 0.0
    
    def test_error_handling(self):
        """Test error handling for malformed requests."""
        # Empty email list
        response = requests.post(f"{self.BASE_URL}/verify/start", json={"emails": []})
        assert response.status_code == 400
        
        # Invalid job ID
        response = requests.get(f"{self.BASE_URL}/verify/nonexistent/tier0")
        assert response.status_code == 404
        
        # Malformed JSON
        response = requests.post(
            f"{self.BASE_URL}/verify/start", 
            data="invalid json",
            headers={"Content-Type": "application/json"}
        )
        assert response.status_code == 422
    
    def poll_for_results(self, job_id: str, expected_count: int, timeout: int = 30) -> List[Dict]:
        """Poll for verification results until expected count is reached."""
        results = []
        last_id = None
        start_time = time.time()
        
        while len(results) < expected_count and time.time() - start_time < timeout:
            poll_payload = {"last_id": last_id, "max": 100}
            
            response = requests.post(f"{self.BASE_URL}/verify/{job_id}/poll", json=poll_payload)
            assert response.status_code == 200
            
            data = response.json()
            new_items = data.get("items", [])
            
            if new_items:
                results.extend(new_items)
                last_id = data.get("last_id")
            else:
                time.sleep(0.5)  # Wait before polling again
        
        return results


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
