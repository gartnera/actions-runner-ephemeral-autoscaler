#!/bin/bash

# Usage:   wait-metric-expr.sh <metric suffix> <value or regex>
# Example: wait-metric-expr.sh idle 1

name=$1
expr=$2

while ! curl -s localhost:9090/metrics | grep -E "^actions_runner_autoscaler_${name}.*${expr}"; do
  echo "Waiting for autoscaler instances to be ready..."
  curl -s localhost:9090/metrics | grep -E '^actions_runner_autoscaler'
  echo ""
  sleep 5
done