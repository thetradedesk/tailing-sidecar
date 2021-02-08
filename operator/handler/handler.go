/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	tailingsidecarv1 "github.com/SumoLogic/tailing-sidecar/operator/api/v1"
	guuid "github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:path=/add-tailing-sidecars-v1-pod,mutating=true,failurePolicy=fail,groups="",resources=pods,verbs=create;update,versions=v1,name=mpod.kb.io

const (
	sidecarEnv             = "PATH_TO_TAIL"
	sidecarContainerName   = "tailing-sidecar%d"
	sidecarContainerPrefix = "tailing-sidecar"

	hostPathDirPath    = "/var/log/tailing-sidecar-fluentbit/%s/%s"
	hostPathVolumeName = "volume-sidecar%d"
	hostPathMountPath  = "/tailing-sidecar/var"
)

var (
	handlerLog   = ctrl.Log.WithName("tailing-sidecar.operator.handler.PodExtender")
	hostPathType = corev1.HostPathDirectoryOrCreate
)

// PodExtender extends Pods by tailling sidecar containers
type PodExtender struct {
	Client              client.Client
	TailingSidecarImage string
	decoder             *admission.Decoder
}

// Handle handle requests to create/update Pod and extend it by adding tailing sidecars
func (e *PodExtender) Handle(ctx context.Context, req admission.Request) admission.Response {
	pod := &corev1.Pod{}
	err := e.decoder.Decode(req, pod)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	handlerLog.Info("Handling request for Pod",
		"Name", pod.ObjectMeta.Name,
		"Namespace", pod.ObjectMeta.Namespace,
		"Operation", req.Operation,
	)

	if _, ok := pod.ObjectMeta.Annotations[sidecarAnnotation]; ok {

		// Get TailingSidecars from namespace
		tailingSidecarList := &tailingsidecarv1.TailingSidecarList{}
		tailingSidecarListOpts := []client.ListOption{
			client.InNamespace(pod.ObjectMeta.Namespace),
		}

		if err := e.Client.List(ctx, tailingSidecarList, tailingSidecarListOpts...); err != nil {
			handlerLog.Error(err,
				"Failed to get list of TailingSidecars in namespace",
				"namespace", pod.ObjectMeta.Namespace)
		}

		// Join configurations from TailingSidecars
		tailingSidecarConfigs := joinTailinSidecarConfigs(tailingSidecarList.Items)

		// Parse configuration from annotation and join them with configurations from TailingSidecars
		configs := getConfigs(pod.ObjectMeta.Annotations, tailingSidecarConfigs)

		hostPathDir := setHostPath(pod)

		// Add tailing sidecars to Pod
		if len(configs) != 0 {
			handlerLog.Info("Found configuration for Pod",
				"Pod Name", pod.ObjectMeta.Name,
				"Namespace", pod.ObjectMeta.Namespace)

			containers := make([]corev1.Container, 0)

			sidecarID := len(getTailingSidecars(pod.Spec.Containers))

			for _, config := range configs {
				if isSidecarAvailable(pod.Spec.Containers, config) {
					// Do not add tailing sidecar if tailing sidecar with specific configuration exists
					handlerLog.Info("Tailing sidecar exists",
						"file", config.File,
						"volume", config.Volume)
					continue
				}

				volume, err := getVolume(pod.Spec.Containers, config.Volume)
				if err != nil {
					handlerLog.Error(err,
						"Failed to find volume",
						"Pod Name", pod.ObjectMeta.Name,
						"Namespace", pod.ObjectMeta.Namespace)
					continue
				}

				volumeName := fmt.Sprintf(hostPathVolumeName, sidecarID)
				containerName := fmt.Sprintf(sidecarContainerName, sidecarID)
				hostPath := fmt.Sprintf("%s/%s", hostPathDir, containerName)
				pod.Spec.Volumes = append(pod.Spec.Volumes,
					corev1.Volume{
						Name: volumeName,
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: hostPath,
								Type: &hostPathType,
							},
						},
					})

				container := corev1.Container{
					Image: e.TailingSidecarImage,
					Name:  containerName,
					Env: []corev1.EnvVar{{
						Name:  sidecarEnv,
						Value: config.File,
					}},
					VolumeMounts: []corev1.VolumeMount{
						volume,
						{
							Name:      volumeName,
							MountPath: hostPathMountPath,
						},
					},
				}
				containers = append(containers, container)
				sidecarID++
			}
			podContainers := removeDeletedSidecars(pod.Spec.Containers, configs)
			pod.Spec.Containers = append(podContainers, containers...)
		}
	}

	marshaledPod, err := json.Marshal(pod)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}

// InjectDecoder injects the decoder.
func (e *PodExtender) InjectDecoder(d *admission.Decoder) error {
	e.decoder = d
	return nil
}
func removeDeletedSidecars(containers []corev1.Container, configs []tailingsidecarv1.SidecarConfig) []corev1.Container {
	podContainers := make([]corev1.Container, 0)
	for _, container := range containers {
		if !strings.HasPrefix(container.Name, sidecarContainerPrefix) {
			podContainers = append(podContainers, container)
		} else {
			for _, config := range configs {
				if isSidecarEnvAvailable(container.Env, config.File) && isVolumeMountAvailable(container.VolumeMounts, config.Volume) {
					podContainers = append(podContainers, container)
				}
			}
		}
	}
	return podContainers
}

func joinTailinSidecarConfigs(tailinSidecars []tailingsidecarv1.TailingSidecar) map[string]tailingsidecarv1.SidecarConfig {
	sidecarConfigs := make(map[string]tailingsidecarv1.SidecarConfig, 0)
	for _, tailitailinSidecar := range tailinSidecars {
		for name, config := range tailitailinSidecar.Spec.Configs {
			sidecarConfigs[name] = config
		}
	}
	return sidecarConfigs
}

func isSidecarAvailable(containers []corev1.Container, config tailingsidecarv1.SidecarConfig) bool {
	for _, container := range containers {
		if strings.HasPrefix(container.Name, sidecarContainerPrefix) &&
			isSidecarEnvAvailable(container.Env, config.File) &&
			isVolumeMountAvailable(container.VolumeMounts, config.Volume) {
			return true
		}
	}
	return false
}

func isSidecarEnvAvailable(envs []corev1.EnvVar, envValue string) bool {
	for _, env := range envs {
		if env.Name == sidecarEnv && env.Value == envValue {
			return true
		}
	}
	return false
}

func isVolumeMountAvailable(volumeMounts []corev1.VolumeMount, volumeName string) bool {
	for _, volumeMount := range volumeMounts {
		if volumeMount.Name == volumeName {
			return true
		}
	}
	return false
}

func getVolume(containers []corev1.Container, volumeName string) (corev1.VolumeMount, error) {
	for _, container := range containers {
		for _, volume := range container.VolumeMounts {
			if volume.Name == volumeName {
				return volume, nil
			}
		}
	}
	return corev1.VolumeMount{}, fmt.Errorf("Volume was not found, volume: %s", volumeName)
}

func getTailingSidecars(containers []corev1.Container) []corev1.Container {
	tailingSidecars := make([]corev1.Container, 0)
	for _, container := range containers {
		if strings.HasPrefix(container.Name, sidecarContainerPrefix) {
			tailingSidecars = append(tailingSidecars, container)
		}
	}
	return tailingSidecars
}

func setHostPath(pod *corev1.Pod) string {
	if pod.ObjectMeta.Namespace != "" && pod.ObjectMeta.Name != "" {
		return fmt.Sprintf(hostPathDirPath, pod.ObjectMeta.Namespace, pod.ObjectMeta.Name)
	}
	return fmt.Sprintf(hostPathDirPath, strings.TrimRight(pod.GenerateName, "-"), guuid.New().String())
}