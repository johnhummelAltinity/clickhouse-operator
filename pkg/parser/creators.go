// Copyright 2019 Altinity Ltd and/or its affiliates. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package parser

import (
	"fmt"

	chiv1 "github.com/altinity/clickhouse-operator/pkg/apis/clickhouse.altinity.com/v1"

	"github.com/golang/glog"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CHICreateObjects returns a map of the k8s objects created based on ClickHouseInstallation Object properties
func CHICreateObjects(chi *chiv1.ClickHouseInstallation) []interface{} {
	list := make([]interface{}, 0)
	list = append(list, createServiceObjects(chi))
	list = append(list, createConfigMapObjects(chi))
	list = append(list, createStatefulSetObjects(chi))

	return list
}

// createConfigMapObjects returns a list of corev1.ConfigMap objects
func createConfigMapObjects(chi *chiv1.ClickHouseInstallation) ConfigMapList {
	configMapList := make(ConfigMapList, 0)
	configMapList = append(
		configMapList,
		createConfigMapObjectsCommon(chi)...,
	)
	configMapList = append(
		configMapList,
		createConfigMapObjectsDeployment(chi)...,
	)
	return configMapList
}

func createConfigMapObjectsCommon(chi *chiv1.ClickHouseInstallation) ConfigMapList {
	var configs configSections

	// commonConfigSections maps section name to section XML config of the following sections:
	// 1. remote servers
	// 2. zookeeper
	// 3. settings
	// 4. listen
	configs.commonConfigSections = make(map[string]string)
	// commonConfigSections maps section name to section XML config of the following sections:
	// 1. users
	// 2. quotas
	// 3. profiles
	configs.commonUsersConfigSections = make(map[string]string)

	includeNonEmpty(configs.commonConfigSections, filenameRemoteServersXML, generateRemoteServersConfig(chi))
	includeNonEmpty(configs.commonConfigSections, filenameZookeeperXML, generateZookeeperConfig(chi))
	includeNonEmpty(configs.commonConfigSections, filenameSettingsXML, generateSettingsConfig(chi))
	includeNonEmpty(configs.commonConfigSections, filenameListenXML, generateListenConfig(chi))

	includeNonEmpty(configs.commonUsersConfigSections, filenameUsersXML, generateUsersConfig(chi))
	includeNonEmpty(configs.commonUsersConfigSections, filenameQuotasXML, generateQuotasConfig(chi))
	includeNonEmpty(configs.commonUsersConfigSections, filenameProfilesXML, generateProfilesConfig(chi))

	// There are two types of configs, kept in ConfigMaps:
	// 1. Common configs - for all resources in the CHI (remote servers, zookeeper setup, etc)
	//    consists of common configs and common users configs
	// 2. Personal configs - macros config
	// configMapList contains all configs so we need deploymentsNum+2 ConfigMap objects
	// personal config for each deployment and +2 for common config + common user config
	configMapList := make(ConfigMapList, 0)

	// ConfigMap common for all resources in CHI
	// contains several sections, mapped as separated config files,
	// such as remote servers, zookeeper setup, etc
	configMapList = append(
		configMapList,
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      CreateConfigMapCommonName(chi.Name),
				Namespace: chi.Namespace,
				Labels: map[string]string{
					ChopGeneratedLabel: chi.Name,
					CHIGeneratedLabel:  chi.Name,
				},
			},
			// Data contains several sections which are to be several xml configs
			Data: configs.commonConfigSections,
		},
	)

	// ConfigMap common for all users resources in CHI
	configMapList = append(
		configMapList,
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      CreateConfigMapCommonUsersName(chi.Name),
				Namespace: chi.Namespace,
				Labels: map[string]string{
					ChopGeneratedLabel: chi.Name,
					CHIGeneratedLabel:  chi.Name,
				},
			},
			// Data contains several sections which are to be several xml configs
			Data: configs.commonUsersConfigSections,
		},
	)

	return configMapList
}

func createConfigMapObjectsDeployment(chi *chiv1.ClickHouseInstallation) ConfigMapList {
	configMapList := make(ConfigMapList, 0)
	replicaProcessor := func(replica *chiv1.ChiClusterLayoutShardReplica) error {
		// Add corev1.Service object to the list
		// Add corev1.ConfigMap object to the list
		configMapList = append(
			configMapList,
			&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      CreateConfigMapMacrosName(replica),
					Namespace: replica.Address.Namespace,
					Labels: map[string]string{
						ChopGeneratedLabel: replica.Address.CHIName,
						CHIGeneratedLabel:  replica.Address.CHIName,
					},
				},
				Data: map[string]string{
					filenameMacrosXML: generateHostMacros(replica),
				},
			},
		)

		return nil
	}
	chi.WalkReplicas(replicaProcessor)

	return configMapList
}

// createServiceObjects returns a list of corev1.Service objects
func createServiceObjects(chi *chiv1.ClickHouseInstallation) ServiceList {
	// We'd like to create "number of deployments" + 1 kubernetes services in order to provide access
	// to each deployment separately and one common predictably-named access point - common service
	serviceList := make(ServiceList, 0)
	serviceList = append(
		serviceList,
		createServiceObjectsCommon(chi)...,
	)
	serviceList = append(
		serviceList,
		createServiceObjectsDeployment(chi)...,
	)

	return serviceList
}

func createServiceObjectsCommon(chi *chiv1.ClickHouseInstallation) ServiceList {
	// Create one predictably-named service to access the whole installation
	// NAME                             TYPE        CLUSTER-IP   EXTERNAL-IP   PORT(S)                      AGE
	// service/clickhouse-replcluster   ClusterIP   None         <none>        9000/TCP,9009/TCP,8123/TCP   1h
	return ServiceList{
		createServiceObjectChi(chi, CreateCHIServiceName(chi.Name)),
	}
}

func createServiceObjectsDeployment(chi *chiv1.ClickHouseInstallation) ServiceList {
	// Create "number of deployments" service - one service for each stateful set
	// Each replica has its stateful set and each stateful set has it service
	// NAME                             TYPE        CLUSTER-IP   EXTERNAL-IP   PORT(S)                      AGE
	// service/chi-01a1ce7dce-2         ClusterIP   None         <none>        9000/TCP,9009/TCP,8123/TCP   1h
	serviceList := make(ServiceList, 0)

	replicaProcessor := func(replica *chiv1.ChiClusterLayoutShardReplica) error {
		// Add corev1.Service object to the list
		serviceList = append(
			serviceList,
			createServiceObjectDeployment(replica),
		)
		return nil
	}
	chi.WalkReplicas(replicaProcessor)

	return serviceList
}

func createServiceObjectChi(
	chi *chiv1.ClickHouseInstallation,
	serviceName string,
) *corev1.Service {
	glog.Infof("createServiceObjectChi() for service %s\n", serviceName)
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: chi.Namespace,
			Labels: map[string]string{
				ChopGeneratedLabel: chi.Name,
				CHIGeneratedLabel:  chi.Name,
			},
		},
		Spec: corev1.ServiceSpec{
			// ClusterIP: templateDefaultsServiceClusterIP,
			Ports: []corev1.ServicePort{
				{
					Name: chDefaultHTTPPortName,
					Port: chDefaultHTTPPortNumber,
				},
				{
					Name: chDefaultClientPortName,
					Port: chDefaultClientPortNumber,
				},
				{
					Name: chDefaultInterServerPortName,
					Port: chDefaultInterServerPortNumber,
				},
			},
			Selector: map[string]string{
				CHIGeneratedLabel: chi.Name,
			},
			Type: "LoadBalancer",
		},
	}
}

func createServiceObjectDeployment(replica *chiv1.ChiClusterLayoutShardReplica) *corev1.Service {
	serviceName := CreateStatefulSetServiceName(replica)
	statefulSetName := CreateStatefulSetName(replica)

	glog.Infof("createServiceObjectDeployment() for service %s %s\n", serviceName, statefulSetName)
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: replica.Address.Namespace,
			Labels: map[string]string{
				ChopGeneratedLabel: replica.Address.CHIName,
				CHIGeneratedLabel:  replica.Address.CHIName,
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name: chDefaultHTTPPortName,
					Port: chDefaultHTTPPortNumber,
				},
				{
					Name: chDefaultClientPortName,
					Port: chDefaultClientPortNumber,
				},
				{
					Name: chDefaultInterServerPortName,
					Port: chDefaultInterServerPortNumber,
				},
			},
			Selector: map[string]string{
				chDefaultAppLabel: statefulSetName,
			},
			ClusterIP: templateDefaultsServiceClusterIP,
			Type:      "ClusterIP",
		},
	}
}

// createStatefulSetObjects returns a list of apps.StatefulSet objects
func createStatefulSetObjects(chi *chiv1.ClickHouseInstallation) StatefulSetList {
	statefulSetList := make(StatefulSetList, 0)

	// Create list of apps.StatefulSet objects
	// StatefulSet is created for each replica.Deployment

	replicaProcessor := func(replica *chiv1.ChiClusterLayoutShardReplica) error {
		glog.Infof("createStatefulSetObjects() for statefulSet %s\n", CreateStatefulSetName(replica))

		// Create and setup apps.StatefulSet object
		statefulSetObject := createStatefulSetObject(replica)
		setupStatefulSetPodTemplate(statefulSetObject, chi, replica)
		setupStatefulSetVolumeClaimTemplate(statefulSetObject, chi, replica)

		// Append apps.StatefulSet to the list of stateful sets
		statefulSetList = append(statefulSetList, statefulSetObject)

		return nil
	}
	chi.WalkReplicas(replicaProcessor)

	return statefulSetList
}

func createStatefulSetObject(replica *chiv1.ChiClusterLayoutShardReplica) *apps.StatefulSet {
	statefulSetName := CreateStatefulSetName(replica)
	serviceName := CreateStatefulSetServiceName(replica)

	// Create apps.StatefulSet object
	replicasNum := int32(1)
	return &apps.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      statefulSetName,
			Namespace: replica.Address.Namespace,
			Labels: map[string]string{
				ChopGeneratedLabel: replica.Address.CHIName,
				CHIGeneratedLabel:  replica.Address.CHIName,
			},
		},
		Spec: apps.StatefulSetSpec{
			Replicas:    &replicasNum,
			ServiceName: serviceName,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					chDefaultAppLabel: statefulSetName,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: statefulSetName,
					Labels: map[string]string{
						chDefaultAppLabel:  statefulSetName,
						ChopGeneratedLabel: replica.Address.CHIName,
						CHIGeneratedLabel:  replica.Address.CHIName,
					},
				},
				Spec: corev1.PodSpec{
					Volumes:    nil,
					Containers: nil,
				},
			},
		},
	}
}

func setupStatefulSetPodTemplate(
	statefulSetObject *apps.StatefulSet,
	chi *chiv1.ClickHouseInstallation,
	replica *chiv1.ChiClusterLayoutShardReplica,
) {
	statefulSetName := CreateStatefulSetName(replica)
	configMapMacrosName := CreateConfigMapMacrosName(replica)

	podTemplatesIndex := createPodTemplatesIndex(chi)
	podTemplate := replica.Deployment.PodTemplate

	configMapCommonName := CreateConfigMapCommonName(replica.Address.CHIName)
	configMapCommonUsersName := CreateConfigMapCommonUsersName(replica.Address.CHIName)

	// Specify pod templates - either explicitly defined or default
	if podTemplateData, ok := podTemplatesIndex[podTemplate]; ok {
		// Replica references known PodTemplate
		copyPodTemplateFrom(statefulSetObject, podTemplateData)
		glog.Infof("createStatefulSetObjects() for statefulSet %s - template: %s\n", statefulSetName, podTemplate)
	} else {
		// Replica references UNKNOWN PodTemplate
		copyPodTemplateFrom(statefulSetObject, createDefaultPodTemplate(statefulSetName))
		glog.Infof("createStatefulSetObjects() for statefulSet %s - default template\n", statefulSetName)
	}

	// And now loop over all containers in this template and
	// append all VolumeMounts which are ConfigMap mounts
	for i := range statefulSetObject.Spec.Template.Spec.Containers {
		// Convenience wrapper
		container := &statefulSetObject.Spec.Template.Spec.Containers[i]
		// Append to each Container current VolumeMount's to VolumeMount's declared in template
		container.VolumeMounts = append(
			container.VolumeMounts,
			createVolumeMountObject(configMapCommonName, dirPathConfigd),
			createVolumeMountObject(configMapCommonUsersName, dirPathUsersd),
			createVolumeMountObject(configMapMacrosName, dirPathConfd),
		)
	}

	// Add all ConfigMap objects as Pod's volumes
	statefulSetObject.Spec.Template.Spec.Volumes = append(
		statefulSetObject.Spec.Template.Spec.Volumes,
		createVolumeObjectConfigMap(configMapCommonName),
		createVolumeObjectConfigMap(configMapCommonUsersName),
		createVolumeObjectConfigMap(configMapMacrosName),
	)
}

func setupStatefulSetVolumeClaimTemplate(
	statefulSetObject *apps.StatefulSet,
	chi *chiv1.ClickHouseInstallation,
	replica *chiv1.ChiClusterLayoutShardReplica,
) {
	statefulSetName := CreateStatefulSetName(replica)
	// Templates index maps template name to (simplified) template itself
	// Used to provide named access to templates
	volumeClaimTemplatesIndex := createVolumeClaimTemplatesIndex(chi)

	volumeClaimTemplateName := replica.Deployment.VolumeClaimTemplate

	// Specify volume claim templates - either explicitly defined or default
	volumeClaimTemplate, ok := volumeClaimTemplatesIndex[volumeClaimTemplateName]
	if !ok {
		// Unknown VolumeClaimTemplate
		glog.Infof("createStatefulSetObjects() for statefulSet %s - no VC templates\n", statefulSetName)
		return
	}

	// Known VolumeClaimTemplate

	statefulSetObject.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{
		volumeClaimTemplate.PersistentVolumeClaim,
	}

	// Add default corev1.VolumeMount section for ClickHouse data
	if volumeClaimTemplate.UseDefaultName {
		statefulSetObject.Spec.Template.Spec.Containers[0].VolumeMounts = append(
			statefulSetObject.Spec.Template.Spec.Containers[0].VolumeMounts,
			corev1.VolumeMount{
				Name:      chDefaultVolumeMountNameData,
				MountPath: dirPathClickHouseData,
			})
		glog.Infof("createStatefulSetObjects() for statefulSet %s - VC template.useDefaultName: %s\n", statefulSetName, volumeClaimTemplateName)
	}
	glog.Infof("createStatefulSetObjects() for statefulSet %s - VC template: %s\n", statefulSetName, volumeClaimTemplateName)
}

func copyPodTemplateFrom(dst *apps.StatefulSet, src *chiv1.ChiPodTemplate) {
	// Prepare .statefulSetObject.Spec.Template.Spec - fill with template's data

	// Setup Container's

	// Copy containers from pod template
	// ... snippet from .spec.templates.podTemplates
	//       containers:
	//      - name: clickhouse
	//        volumeMounts:
	//        - name: clickhouse-data-test
	//          mountPath: /var/lib/clickhouse
	//        image: yandex/clickhouse-server:18.16.2
	dst.Spec.Template.Spec.Containers = make([]corev1.Container, len(src.Containers))
	copy(dst.Spec.Template.Spec.Containers, src.Containers)

	// Setup Volume's
	// Copy volumes from pod template
	dst.Spec.Template.Spec.Volumes = make([]corev1.Volume, len(src.Volumes))
	copy(dst.Spec.Template.Spec.Volumes, src.Volumes)
}

// createDefaultPodTemplate returns default podTemplatesIndexData
func createDefaultPodTemplate(name string) *chiv1.ChiPodTemplate {
	return &chiv1.ChiPodTemplate{
		Name: "createDefaultPodTemplate",
		Containers: []corev1.Container{
			{
				Name:  name,
				Image: chDefaultDockerImage,
				Ports: []corev1.ContainerPort{
					{
						Name:          chDefaultHTTPPortName,
						ContainerPort: chDefaultHTTPPortNumber,
					},
					{
						Name:          chDefaultClientPortName,
						ContainerPort: chDefaultClientPortNumber,
					},
					{
						Name:          chDefaultInterServerPortName,
						ContainerPort: chDefaultInterServerPortNumber,
					},
				},
			},
		},
		Volumes: []corev1.Volume{},
	}
}

// createVolumeObjectConfigMap returns corev1.Volume object with defined name
func createVolumeObjectConfigMap(name string) corev1.Volume {
	return corev1.Volume{
		Name: name,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: name,
				},
			},
		},
	}
}

// createVolumeMountObject returns corev1.VolumeMount object with name and mount path
func createVolumeMountObject(name, mountPath string) corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      name,
		MountPath: mountPath,
	}
}

// createVolumeClaimTemplatesIndex returns a map of volumeClaimTemplatesIndexData used as a reference storage for VolumeClaimTemplates
func createVolumeClaimTemplatesIndex(chi *chiv1.ClickHouseInstallation) volumeClaimTemplatesIndex {
	index := make(volumeClaimTemplatesIndex)
	for i := range chi.Spec.Templates.VolumeClaimTemplates {
		// Convenience wrapper
		volumeClaimTemplate := &chi.Spec.Templates.VolumeClaimTemplates[i]

		if volumeClaimTemplate.PersistentVolumeClaim.Name == useDefaultPersistentVolumeClaimMacro {
			volumeClaimTemplate.PersistentVolumeClaim.Name = chDefaultVolumeMountNameData
			volumeClaimTemplate.UseDefaultName = true
		}
		index[volumeClaimTemplate.Name] = volumeClaimTemplate
	}

	return index
}

// createPodTemplatesIndex returns a map of podTemplatesIndexData used as a reference storage for PodTemplates
func createPodTemplatesIndex(chi *chiv1.ClickHouseInstallation) podTemplatesIndex {
	index := make(podTemplatesIndex)
	for i := range chi.Spec.Templates.PodTemplates {
		// Convenience wrapper
		podTemplate := &chi.Spec.Templates.PodTemplates[i]
		index[podTemplate.Name] = podTemplate
	}

	return index
}

// CreateConfigMapMacrosName returns a name for a ConfigMap (CH macros) resource based on predefined pattern
func CreateConfigMapMacrosName(replica *chiv1.ChiClusterLayoutShardReplica) string {
	return fmt.Sprintf(
		configMapMacrosNamePattern,
		replica.Address.CHIName,
		replica.Address.ClusterIndex,
		replica.Address.ShardIndex,
		replica.Address.ReplicaIndex,
	)
}

// CreateConfigMapCommonName returns a name for a ConfigMap resource based on predefined pattern
func CreateConfigMapCommonName(chiName string) string {
	return fmt.Sprintf(configMapCommonNamePattern, chiName)
}

// CreateConfigMapCommonUsersName returns a name for a ConfigMap resource based on predefined pattern
func CreateConfigMapCommonUsersName(chiName string) string {
	return fmt.Sprintf(configMapCommonUsersNamePattern, chiName)
}

// CreateInstServiceName creates a name of a Installation Service resource
// prefix is a fullDeploymentID
func CreateCHIServiceName(prefix string) string {
	return fmt.Sprintf(chiServiceNamePattern, prefix)
}

// CreateStatefulSetName creates a name of a StatefulSet resource
// prefix is a fullDeploymentID
func CreateStatefulSetName(replica *chiv1.ChiClusterLayoutShardReplica) string {
	return fmt.Sprintf(
		statefulSetNamePattern,
		replica.Address.CHIName,
		replica.Address.ClusterIndex,
		replica.Address.ShardIndex,
		replica.Address.ReplicaIndex,
	)
}

// CreateStatefulSetServiceName creates a name of a pod Service resource
// prefix is a fullDeploymentID
func CreateStatefulSetServiceName(replica *chiv1.ChiClusterLayoutShardReplica) string {
	return fmt.Sprintf(
		statefulSetServiceNamePattern,
		replica.Address.CHIName,
		replica.Address.ClusterIndex,
		replica.Address.ShardIndex,
		replica.Address.ReplicaIndex,
	)
}

// CreatePodHostname creates a name of a Pod resource
// prefix is a fullDeploymentID
// ss-1eb454-2-0
func CreatePodHostname(replica *chiv1.ChiClusterLayoutShardReplica) string {
	return fmt.Sprintf(
		podHostnamePattern,
		replica.Address.CHIName,
		replica.Address.ClusterIndex,
		replica.Address.ShardIndex,
		replica.Address.ReplicaIndex,
	)
}

// CreateNamespaceDomainName creates domain name of a namespace
// .my-dev-namespace.svc.cluster.local
func CreateNamespaceDomainName(chiNamespace string) string {
	return fmt.Sprintf(namespaceDomainPattern, chiNamespace)
}

// CreatePodFQDN creates a fully qualified domain name of a pod
// prefix is a fullDeploymentID
// ss-1eb454-2-0.my-dev-domain.svc.cluster.local
func CreatePodFQDN(chiNamespace, prefix string) string {
	return fmt.Sprintf(
		podFQDNPattern,
		prefix,
		chiNamespace,
	)
}
