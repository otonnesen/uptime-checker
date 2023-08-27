# Simple uptime checker

A simple service written (in a couple hours, hence the mess!) to send periodic HTTP requests and log when their responses look unexpected. Controllable via HTTP (see below). The service doesn't currently persist/display the uptime status anywhere except in its log output to stdout.

# Sample usage:
```bash
# Create some jobs
curl -XPOST localhost:8081/jobs/ -d '{"url":"https://google.com","method":"GET","expected_status":200,"frequency":"2m"}'
# {
#   "id": 1,
#   "url": "https://google.com",
#   "method": "GET",
#   "expected_status": 200,
#   "frequency": "2m0s"
# }
curl -XPOST localhost:8081/jobs/ -d '{"url":"https://status.sendgrid.com","method":"GET","expected_status":200,"frequency":"30s","jq_query":{"query":".components[] | select(.name == \"API\") | .status","expectation":"operational"}}'
# {
#   "id": 2,
#   "url": "https://status.sendgrid.com",
#   "method": "GET",
#   "expected_status": 200,
#   "frequency": "30s",
#   "jq_query": {
#     "query": ".components[] | select(.name == \"API\") | .status",
#     "expectation": "operational"
#   }
# }
curl -XPOST localhost:8081/jobs/ -d '{"url":"https://catfact.ninja/fact","method":"GET","expected_status":200,"frequency":"45s","jq_query":{"query":"keys[0]","expectation":"fact"}}'
# {
#   "id": 3,
#   "url": "https://catfact.ninja/fact",
#   "method": "GET",
#   "expected_status": 200,
#   "frequency": "30s"
#   "jq_query": {
#     "query": "keys[0]",
#     "expectation": "length"
#   }
# }

# List active jobs
curl -XGET localhost:8081/jobs/
# [
#   {
#     "id": 1,
#     "url": "https://google.com",
#     "method": "GET",
#     "expected_status": 200,
#     "frequency": "2m0s"
#   },
#   {
#     "id": 2,
#     "url": "https://status.sendgrid.com",
#     "method": "GET",
#     "expected_status": 200,
#     "frequency": "30s",
#     "jq_query": {
#       "query": ".components[] | select(.name == \"API\") | .status",
#       "expectation": "operational"
#     }
#   },
#   {
#     "id": 3,
#     "url": "https://catfact.ninja/fact",
#     "method": "GET",
#     "expected_status": 200,
#     "frequency": "30s"
#     "jq_query": {
#       "query": "keys[0]",
#       "expectation": "length"
#     }
#   }
# ]

# Update a job
curl -XPUT localhost:8081/jobs/1 -d '{"url":"https://google.com","method":"GET","expected_status":200,"frequency":"5m"}'
# {
#   "url": "https://google.com",
#   "method": "GET",
#   "expected_status": 200,
#   "frequency": "5m0s"
# }

# Remove a job
curl -XDELETE localhost:8081/jobs/1
curl -XGET localhost:8081/jobs/
# [
#   {
#     "id": 2,
#     "url": "https://status.sendgrid.com",
#     "method": "GET",
#     "expected_status": 200,
#     "frequency": "30s",
#     "jq_query": {
#       "query": ".components[] | select(.name == \"API\") | .status",
#       "expectation": "operational"
#     }
#   },
#   {
#     "id": 3,
#     "url": "https://catfact.ninja/fact",
#     "method": "GET",
#     "expected_status": 200,
#     "frequency": "30s"
#     "jq_query": {
#       "query": "keys[0]",
#       "expectation": "length"
#     }
#   }
# ]
```
