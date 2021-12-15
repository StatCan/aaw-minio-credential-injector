package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"k8s.io/api/admission/v1beta1"
)

type Instance struct {
	Name           string
	Alias          string
	Classification string
	ServiceUrl     string
	ExternalUrl    string
}

var instances []Instance
var defaultInstances = `
	{"name": "minio_standard", "alias": "", "classification": "unclassified", "serviceUrl": "http://minio.minio-standard-system:443", "externalUrl": "https://minio-standard.aaw-dev.cloud.statcan.ca"}
	{"name": "minio_premium", "alias": "", "classification": "unclassified", "serviceUrl": "http://minio.minio-premium-system:443", "externalUrl": "https://minio-premium.aaw-dev.cloud.statcan.ca"}
	{"name": "minio_protected_b", "alias": "", "classification": "protected-b", "serviceUrl": "http://minio.minio-protected-b-system:443", "externalUrl": ""}
`

// Based on https://medium.com/ovni/writing-a-very-basic-kubernetes-mutating-admission-webhook-398dbbcb63ec
func handleRoot(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Hello, world!")
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "ok")
}

func handleMutate(w http.ResponseWriter, r *http.Request) {
	// Decode the request
	body, err := ioutil.ReadAll(r.Body)
	defer r.Body.Close()
	if err != nil {
		log.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "%s", err)
		return
	}

	admissionReview := v1beta1.AdmissionReview{}
	if err := json.Unmarshal(body, &admissionReview); err != nil {
		log.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "%s", err)
		return
	}

	response, err := mutate(*admissionReview.Request, instances)
	if err != nil {
		log.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "%s", err)
		return
	}

	reviewResponse := v1beta1.AdmissionReview{
		Response: &response,
	}

	if body, err = json.Marshal(reviewResponse); err != nil {
		log.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "%s", err)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// Sets the global instances variable
func configInstances() {
	var config string
	if _, err := os.Stat("instances.json"); os.IsNotExist(err) {
		config = defaultInstances
	} else {
		config_bytes, err := ioutil.ReadFile("instances.json") // just pass the file name
		if err != nil {
			log.Fatal(err)
		}
		config = string(config_bytes)
	}

	dec := json.NewDecoder(strings.NewReader(config))
	for {
		var instance Instance
		err := dec.Decode(&instance)
		if err != nil {
			if err == io.EOF {
				break
			}
			log.Fatal(err)
		}
		fmt.Println(instance)
		instances = append(instances, instance)
	}
}

func main() {

	// Configure the MinIO Instances
	configInstances()

	mux := http.NewServeMux()

	mux.HandleFunc("/", handleRoot)
	mux.HandleFunc("/_healthz", handleHealthz)
	mux.HandleFunc("/mutate", handleMutate)

	s := &http.Server{
		Addr:           ":8443",
		Handler:        mux,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	log.Println("Listening on :8443")
	log.Fatal(s.ListenAndServeTLS("./certs/tls.crt", "./certs/tls.key"))
}
