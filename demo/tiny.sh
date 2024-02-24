#!/usr/bin/env sh

kubectl exec -it $(kubectl get po -l app.kubernetes.io/name=tiny -o name) -- ./tiny -c tiny.yml
