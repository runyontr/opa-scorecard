package opa

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	controllerClient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ConstraintMeta represents meta information of a constraint
type ConstraintMeta struct {
	Kind string
	Name string
}

// Violation represents each constraintViolation under status
type Violation struct {
	Kind              string `json:"kind"`
	Name              string `json:"name"`
	Namespace         string `json:"namespace,omitempty"`
	Message           string `json:"message"`
	EnforcementAction string `json:"enforcementAction"`
}

type ConstraintStatus struct {
	TotalViolations float64 `json:"totalViolations,omitempty"`
	Violations      []*Violation
}

// Constraint
type Constraint struct {
	Meta   ConstraintMeta
	Spec   ConstraintSpec
	Status ConstraintStatus
}

// ConstraintSpec collect general information about the overall constraints applied to the cluster
type ConstraintSpec struct {
	EnforcementAction string `json:"enforcementAction"`
}

const (
	constraintsGV           = "constraints.gatekeeper.sh/v1beta1"
	constraintsGroup        = "constraints.gatekeeper.sh"
	constraintsGroupVersion = "v1beta1"
)

func createConfig(inCluster *bool) (*restclient.Config, error) {
	if *inCluster {
		log.Println("Using incluster K8S client")
		return rest.InClusterConfig()
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Println("Could not find user HomeDir" + err.Error())
			return nil, err
		}

		kubeconfig := filepath.Join(home, ".kube", "config")

		// use the current context in kubeconfig
		config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			log.Println(err)
			return nil, err
		}
		return config, nil
	}
}

func createKubeClient(inCluster *bool) (*kubernetes.Clientset, error) {

	config, err := createConfig(inCluster)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	// create the clientset
	clientset, err := kubernetes.NewForConfig(config)

	if err != nil {
		log.Println(err)
		return nil, err
	}
	return clientset, nil
}

func createKubeClientGroupVersion(inCluster *bool) (controllerClient.Client, error) {
	config, err := createConfig(inCluster)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	client, err := controllerClient.New(config, controllerClient.Options{})
	if err != nil {
		log.Println(err)
		return nil, err
	}
	return client, nil
}

func print(o interface{}) {
	b, err := json.MarshalIndent(o, "", "\t")
	if err != nil {
		log.Println("Error marshalling: %+v\n", err)
	} else {
		log.Println(string(b))
	}
}

var (
	runtimeClassGVR = schema.GroupVersionResource{
		Group:    "constraints.gatekeeper.sh.",
		Version:  "v1beta",
		Resource: "k8sallowedrepos",
	}
)

// GetConstraints returns a list of all OPA constraints
func GetConstraints(inCluster *bool) ([]Constraint, error) {
	client, err := createKubeClient(inCluster)
	if err != nil {
		return nil, err
	}
	print(client)

	cClient, err := createKubeClientGroupVersion(inCluster)
	if err != nil {
		return nil, err
	}
	print(cClient)
	_, c, err := client.ServerGroupsAndResources()
	// c, err := client.ServerResourcesForGroupVersion(constraintsGV)
	if err != nil {
		return nil, err
	}
	// print(c)

	var constraints []Constraint
	for _, apiresources := range c {
		if apiresources.GroupVersion != constraintsGV {
			log.Println("Skipping group ", apiresources.GroupVersion)
			continue
		}
		for _, r := range apiresources.APIResources {
			canList := false

			if strings.HasSuffix(r.Name, "/status") {
				continue
			}

			for _, verb := range r.Verbs {
				if verb == "list" {
					canList = true
					break
				}
			}

			if !canList {
				log.Printf("Can't list objets of type %+v\n", r.Name)
				for _, verb := range r.Verbs {
					log.Println("Allowed: ", verb)
				}
				continue
			}
			actual := &unstructured.UnstructuredList{}
			actual.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   r.Group,
				Kind:    r.Kind,
				Version: constraintsGV,
			})

			err = cClient.List(context.Background(), actual)
			if err != nil {
				log.Printf("Error listing: %+v\n", err)
				continue
			}

			if len(actual.Items) > 0 {
				for _, item := range actual.Items {
					kind := item.GetKind()
					name := item.GetName()
					namespace := item.GetNamespace()
					log.Printf("Kind:%s, Name:%s, Namespace:%s \n", kind, name, namespace)
					var obj = item.Object
					var constraint Constraint
					data, err := json.Marshal(obj)
					if err != nil {
						log.Println(err)
						continue
					}
					err = json.Unmarshal(data, &constraint)
					if err != nil {
						log.Println(err)
						continue
					}

					constraints = append(constraints, Constraint{
						Meta:   ConstraintMeta{Kind: item.GetKind(), Name: item.GetName()},
						Status: ConstraintStatus{TotalViolations: constraint.Status.TotalViolations, Violations: constraint.Status.Violations},
						Spec:   ConstraintSpec{EnforcementAction: constraint.Spec.EnforcementAction},
					})
				}
			} else {
				log.Println("Nothing returned for Kind ", r.Kind)
			}

		}
	}
	return constraints, nil
}
