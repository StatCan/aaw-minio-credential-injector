# minio-credential-injector

This mutating webhook adds minio credential annotations to notebook pods and argo workflows (used by Kubeflow Pipelines).

To configure use with different instances, put a `instances.json` file in the working directory. For example

```json
{"name": "minio_standard", "classification": "unclassified", "serviceUrl": "http://minio.minio-standard-system:443"}
{"name": "minio_premium", "classification": "unclassified", "serviceUrl": "http://minio.minio-premium-system:443"}
```

Try it with

```sh
./minio-credential-injector &
curl --insecure -X POST  -H "Content-Type: application/json" \
    -d @samples/pod.json https://0.0.0.0:8443/mutate | 
        jq -r '.response.patch | @base64d' | jq
```
