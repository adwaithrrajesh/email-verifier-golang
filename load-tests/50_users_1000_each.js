/**
 * K6 Load Test Script
 * Simulates 50 concurrent users verifying 1000 emails each
 * Target: 50,000 total verifications in 5 minutes (300 verifications/sec)
 */

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Counter, Trend } from 'k6/metrics';

// Custom metrics
const verificationRate = new Rate('verification_success_rate');
const verificationCounter = new Counter('verifications_total');
const verificationLatency = new Trend('verification_latency');

// Test configuration
export const options = {
  stages: [
    { duration: '30s', target: 10 },  // Ramp up to 10 users
    { duration: '1m', target: 25 },   // Ramp up to 25 users
    { duration: '1m', target: 50 },   // Ramp up to 50 users
    { duration: '5m', target: 50 },   // Stay at 50 users for 5 minutes
    { duration: '30s', target: 0 },   // Ramp down
  ],
  thresholds: {
    http_req_duration: ['p(95)<5000'], // 95% of requests under 5s
    verification_success_rate: ['rate>0.95'], // 95% success rate
    verification_latency: ['p(95)<10000'], // 95% of verifications under 10s
  },
};

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const EMAILS_PER_USER = parseInt(__ENV.EMAILS_PER_USER || '1000');
const BATCH_SIZE = parseInt(__ENV.BATCH_SIZE || '50');

// Generate test email addresses
function generateTestEmails(count, userIndex) {
  const emails = [];
  const domains = [
    'example.com',
    'test-domain.org', 
    'business.co.uk',
    'gmail.com',
    'yahoo.com',
    'outlook.com',
    'company.net',
    'startup.io'
  ];
  
  for (let i = 0; i < count; i++) {
    const domain = domains[i % domains.length];
    const email = `user${userIndex}_${i}@${domain}`;
    emails.push(email);
  }
  
  return emails;
}

// Poll for verification results
function pollForResults(jobId, expectedCount, maxWaitTime = 60) {
  const startTime = Date.now();
  let results = [];
  let lastId = null;
  
  while (results.length < expectedCount && (Date.now() - startTime) < maxWaitTime * 1000) {
    const pollPayload = {
      last_id: lastId,
      max: 200
    };
    
    const pollResponse = http.post(
      `${BASE_URL}/verify/${jobId}/poll`,
      JSON.stringify(pollPayload),
      {
        headers: { 'Content-Type': 'application/json' },
        timeout: '10s'
      }
    );
    
    if (pollResponse.status === 200) {
      const pollData = JSON.parse(pollResponse.body);
      const newItems = pollData.items || [];
      
      if (newItems.length > 0) {
        results = results.concat(newItems);
        lastId = pollData.last_id;
      } else {
        sleep(0.5); // Wait before polling again
      }
    } else {
      console.error(`Poll failed: ${pollResponse.status}`);
      break;
    }
  }
  
  return results;
}

export default function () {
  const userIndex = __VU; // K6 virtual user index
  
  // Generate emails for this user
  const testEmails = generateTestEmails(EMAILS_PER_USER, userIndex);
  
  // Process emails in batches
  for (let batchStart = 0; batchStart < testEmails.length; batchStart += BATCH_SIZE) {
    const batchEnd = Math.min(batchStart + BATCH_SIZE, testEmails.length);
    const emailBatch = testEmails.slice(batchStart, batchEnd);
    
    const startTime = Date.now();
    
    // Start verification
    const payload = {
      emails: emailBatch,
      sender: 'noreply@load-test.local',
      job_id: `load_test_${userIndex}_${batchStart}`
    };
    
    const startResponse = http.post(
      `${BASE_URL}/verify/start`,
      JSON.stringify(payload),
      {
        headers: { 'Content-Type': 'application/json' },
        timeout: '30s'
      }
    );
    
    const startSuccess = check(startResponse, {
      'verification started': (r) => r.status === 200,
      'job_id returned': (r) => {
        if (r.status === 200) {
          const data = JSON.parse(r.body);
          return data.job_id !== undefined;
        }
        return false;
      }
    });
    
    if (!startSuccess) {
      console.error(`Failed to start verification: ${startResponse.status} ${startResponse.body}`);
      continue;
    }
    
    const startData = JSON.parse(startResponse.body);
    const jobId = startData.job_id;
    
    // Get tier0 results (fast pre-filtering)
    const tier0Response = http.get(`${BASE_URL}/verify/${jobId}/tier0`, {
      timeout: '10s'
    });
    
    check(tier0Response, {
      'tier0 results available': (r) => r.status === 200
    });
    
    // Poll for final results
    const results = pollForResults(jobId, emailBatch.length, 60);
    
    const endTime = Date.now();
    const totalLatency = endTime - startTime;
    
    // Record metrics
    verificationCounter.add(results.length);
    verificationLatency.add(totalLatency);
    
    const successCount = results.filter(r => 
      r.status === 'valid' || r.status === 'invalid' || r.status === 'unknown'
    ).length;
    
    verificationRate.add(successCount === emailBatch.length);
    
    // Check results quality
    check(results, {
      'all emails processed': (r) => r.length === emailBatch.length,
      'results have required fields': (r) => {
        return r.every(result => 
          result.email && 
          result.status && 
          result.confidence !== undefined
        );
      },
      'confidence scores valid': (r) => {
        return r.every(result => 
          result.confidence >= 0 && result.confidence <= 1
        );
      }
    });
    
    // Log progress
    if (batchStart % (BATCH_SIZE * 5) === 0) {
      console.log(`User ${userIndex}: Processed ${batchStart + emailBatch.length}/${EMAILS_PER_USER} emails`);
    }
    
    // Small delay between batches to avoid overwhelming the system
    sleep(0.1);
  }
}

// Setup function - runs once per VU at the beginning
export function setup() {
  // Check if the service is available
  const healthResponse = http.get(`${BASE_URL}/health`, { timeout: '10s' });
  
  if (healthResponse.status !== 200) {
    throw new Error(`Service not available: ${healthResponse.status}`);
  }
  
  console.log('Load test starting - service is healthy');
  
  return {
    baseUrl: BASE_URL,
    emailsPerUser: EMAILS_PER_USER,
    batchSize: BATCH_SIZE
  };
}

// Teardown function - runs once at the end
export function teardown(data) {
  // Get final metrics
  const metricsResponse = http.get(`${BASE_URL}/metrics`, { timeout: '10s' });
  
  if (metricsResponse.status === 200) {
    const metrics = JSON.parse(metricsResponse.body);
    console.log('Final system metrics:', JSON.stringify(metrics, null, 2));
  }
  
  console.log('Load test completed');
}
