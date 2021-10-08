package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"k8s.io/api/admission/v1beta1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func cleanName(name string) string {
	return strings.ReplaceAll(name, "_", "-")
}

func useExternalVault(pod *v1.Pod) (bool, string) {
	if os.Getenv("VAULT_ADDR_HTTPS") == "" {
		return false, ""
	}

	// if val, ok := pod.ObjectMeta.Labels["sidecar.istio.io/inject"]; ok && val == "false" {
	if _, ok := pod.ObjectMeta.Labels["workflows.argoproj.io/workflow"]; ok {
		log.Printf("Will use external Vault address for workflow %s", pod.Name)
		return true, os.Getenv("VAULT_ADDR_HTTPS")
	}

	return false, ""
}

func shouldInject(pod *v1.Pod) bool {

	// Inject Minio credentials into notebook pods (condition: has notebook-name label)
	if _, ok := pod.ObjectMeta.Labels["notebook-name"]; ok {
		log.Printf("Found notebook name for %s/%s; injecting", pod.Namespace, pod.Name)
		return true
	}

	// Inject Minio credentials into argo workflow pods (condition: has workflows.argoproj.io/workflow label)
	if _, ok := pod.ObjectMeta.Labels["workflows.argoproj.io/workflow"]; ok {
		log.Printf("Found argo workflow name for %s/%s; injecting", pod.Namespace, pod.Name)
		return true
	}

	// Inject Minio credentials into pod requesting credentials (condition: has add-default-minio-creds annotation)
	if _, ok := pod.ObjectMeta.Annotations["data.statcan.gc.ca/inject-minio-creds"]; ok {
		log.Printf("Found minio credential annotation on %s/%s; injecting", pod.Namespace, pod.Name)
		return true
	}

	return false
}

func mutate(request v1beta1.AdmissionRequest, instances []Instance) (v1beta1.AdmissionResponse, error) {
	response := v1beta1.AdmissionResponse{}

	// Default response
	response.Allowed = true
	response.UID = request.UID

	// Decode the pod object
	var err error
	pod := v1.Pod{}
	if err := json.Unmarshal(request.Object.Raw, &pod); err != nil {
		return response, fmt.Errorf("unable to decode Pod %w", err)
	}

	// Identify the data classification of the pod, defaulting to unclassified if unset
	dataClassification := "unclassified"
	if val, ok := pod.ObjectMeta.Labels["data.statcan.gc.ca/classification"]; ok {
		dataClassification = val
	}

	if shouldInject(&pod) {
		patch := v1beta1.PatchTypeJSONPatch
		response.PatchType = &patch

		response.AuditAnnotations = map[string]string{
			"minio-admission-controller": "Added minio credentials",
		}

		// Handle https://github.com/StatCan/aaw-minio-credential-injector/issues/10
		var roleName string
		if pod.Namespace != "" {
			roleName = cleanName("profile-" + pod.Namespace)
		} else if request.Namespace != "" {
			roleName = cleanName("profile-" + request.Namespace)
		} else {
			return response, fmt.Errorf("pod and request namespace were empty. Cannot determine the namespace.")
		}

		patches := []map[string]interface{}{
			{
				"op":    "add",
				"path":  "/metadata/annotations/vault.hashicorp.com~1agent-inject",
				"value": "true",
			},

			{
				"op":    "add",
				"path":  "/metadata/annotations/vault.hashicorp.com~1agent-pre-populate",
				"value": "false",
			},

			{
				"op":    "add",
				"path":  "/metadata/annotations/vault.hashicorp.com~1role",
				"value": roleName,
			},
		}

		if useExternal, vaultAddr := useExternalVault(&pod); useExternal {
			patches = append(patches, map[string]interface{}{
				"op":    "add",
				"path":  fmt.Sprintf("/metadata/annotations/vault.hashicorp.com~1service"),
				"value": vaultAddr,
			})
		}

		for _, instance := range instances {

			// Only apply to the relevant instances
			if instance.Classification != dataClassification {
				continue
			}

			instanceId := strings.ReplaceAll(instance.Name, "_", "-")
			patches = append(patches, map[string]interface{}{
				"op":    "add",
				"path":  fmt.Sprintf("/metadata/annotations/vault.hashicorp.com~1agent-inject-secret-%s", instanceId),
				"value": fmt.Sprintf("%s/keys/%s", instance.Name, roleName),
			})

			patches = append(patches, map[string]interface{}{
				"op":   "add",
				"path": fmt.Sprintf("/metadata/annotations/vault.hashicorp.com~1agent-inject-template-%s", instanceId),
				"value": fmt.Sprintf(`
{{- with secret "%s/keys/%s" }}
export MINIO_URL="%s"
export MINIO_ACCESS_KEY="{{ .Data.accessKeyId }}"
export MINIO_SECRET_KEY="{{ .Data.secretAccessKey }}"
export AWS_ACCESS_KEY_ID="{{ .Data.accessKeyId }}"
export AWS_SECRET_ACCESS_KEY="{{ .Data.secretAccessKey }}"
{{- end }}
`, instance.Name, roleName, instance.ServiceUrl),
			})

			patches = append(patches, map[string]interface{}{
				"op":    "add",
				"path":  fmt.Sprintf("/metadata/annotations/vault.hashicorp.com~1agent-inject-secret-%s.json", instanceId),
				"value": fmt.Sprintf("%s/keys/%s", instance.Name, roleName),
			})

			patches = append(patches, map[string]interface{}{
				"op":   "add",
				"path": fmt.Sprintf("/metadata/annotations/vault.hashicorp.com~1agent-inject-template-%s.json", instanceId),
				"value": fmt.Sprintf(`
{{- with secret "%s/keys/%s" }}
{
	"MINIO_URL": "%s",
	"MINIO_ACCESS_KEY": "{{ .Data.accessKeyId }}",
	"MINIO_SECRET_KEY": "{{ .Data.secretAccessKey }}",
	"AWS_ACCESS_KEY_ID": "{{ .Data.accessKeyId }}",
	"AWS_SECRET_ACCESS_KEY": "{{ .Data.secretAccessKey }}"
}
{{- end }}
`, instance.Name, roleName, instance.ServiceUrl),
			})
		}

		response.Patch, err = json.Marshal(patches)
		if err != nil {
			return response, err
		}

		response.Result = &metav1.Status{
			Status: metav1.StatusSuccess,
		}
	} else {
		log.Printf("Not injecting the pod %s/%s", pod.Namespace, pod.Name)
	}

	return response, nil
}
