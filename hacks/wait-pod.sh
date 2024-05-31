#!/bin/bash

# Variables
NAMESPACE=$2
NAMESPACE=${NAMESPACE:-"default"}    
POD_NAME=$1      
TIMEOUT=300     
INTERVAL=2       
echo "$POD_NAME/$NAMESPACE"
is_pod_running() {
  kubectl get pod "$POD_NAME" -n "$NAMESPACE" -o jsonpath='{.status.phase}' 2>/dev/null
}

start_time=$(date +%s)
while true; do
  status=$(is_pod_running)
  
  if [ "$status" == "Running" ]; then
    echo "Pod $POD_NAME is running."
    break
  elif [ "$status" == "Failed" ]; then
    echo "Pod $POD_NAME has failed."
    exit 1
  fi

  current_time=$(date +%s)
  elapsed_time=$((current_time - start_time))
  if [ $elapsed_time -ge $TIMEOUT ]; then
    echo "Timeout waiting for pod $POD_NAME to be running."
    exit 1
  fi

  echo "Waiting for pod $NAMESPACE/$POD_NAME to be running..."
  sleep $INTERVAL
done

