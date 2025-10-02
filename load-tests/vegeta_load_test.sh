#!/bin/bash

# Vegeta Load Test Script
# Alternative to K6 for HTTP load testing

set -e

BASE_URL="${BASE_URL:-http://localhost:8080}"
RATE="${RATE:-300}"  # requests per second
DURATION="${DURATION:-5m}"
OUTPUT_DIR="./load-test-results"

echo "Starting Vegeta load test..."
echo "Base URL: $BASE_URL"
echo "Rate: $RATE req/sec"
echo "Duration: $DURATION"

# Create output directory
mkdir -p "$OUTPUT_DIR"

# Check if service is healthy
echo "Checking service health..."
curl -f "$BASE_URL/health" || {
    echo "Service is not healthy. Exiting."
    exit 1
}

# Generate test data
cat > "$OUTPUT_DIR/test_emails.json" << EOF
{
  "emails": [
    "user1@example.com",
    "user2@test-domain.org", 
    "admin@business.co.uk",
    "test@gmail.com",
    "support@yahoo.com",
    "info@outlook.com",
    "sales@company.net",
    "contact@startup.io",
    "noreply@disposable.com",
    "fake@10minutemail.com"
  ],
  "sender": "noreply@load-test.local"
}
EOF

# Create Vegeta target file
cat > "$OUTPUT_DIR/targets.txt" << EOF
POST $BASE_URL/verify/start
Content-Type: application/json
@$OUTPUT_DIR/test_emails.json

EOF

echo "Running Vegeta attack..."

# Run the load test
vegeta attack \
    -targets="$OUTPUT_DIR/targets.txt" \
    -rate="$RATE" \
    -duration="$DURATION" \
    -output="$OUTPUT_DIR/results.bin"

echo "Generating reports..."

# Generate text report
vegeta report < "$OUTPUT_DIR/results.bin" > "$OUTPUT_DIR/report.txt"

# Generate histogram
vegeta report -type=hist[0,2ms,4ms,6ms] < "$OUTPUT_DIR/results.bin" > "$OUTPUT_DIR/histogram.txt"

# Generate plot (if gnuplot is available)
if command -v gnuplot &> /dev/null; then
    vegeta plot < "$OUTPUT_DIR/results.bin" > "$OUTPUT_DIR/plot.html"
    echo "Plot generated: $OUTPUT_DIR/plot.html"
fi

# Display results
echo "=== LOAD TEST RESULTS ==="
cat "$OUTPUT_DIR/report.txt"

echo ""
echo "=== LATENCY HISTOGRAM ==="
cat "$OUTPUT_DIR/histogram.txt"

# Check system metrics after test
echo ""
echo "=== FINAL SYSTEM METRICS ==="
curl -s "$BASE_URL/metrics" | jq '.' || curl -s "$BASE_URL/metrics"

echo ""
echo "Load test completed. Results saved to: $OUTPUT_DIR"
