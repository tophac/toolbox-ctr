/*
 * Copyright © 2019 – 2022 Red Hat Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *    http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package podman

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/HarryMichal/go-version"
	"github.com/containers/toolbox/pkg/shell"
	"github.com/containers/toolbox/pkg/utils"
	"github.com/sirupsen/logrus"
)

type Image struct {
	ID     string
	Names  []string
	Size   string
	Labels map[string]string
}

type ImageSlice []Image

var (
	podmanVersion string
)

var (
	LogLevel = logrus.ErrorLevel
)

func (image *Image) FlattenNames(fillNameWithID bool) []Image {
	var ret []Image

	if len(image.Names) == 0 {
		flattenedImage := *image

		if fillNameWithID {
			shortID := utils.ShortID(image.ID)
			flattenedImage.Names = []string{shortID}
		} else {
			flattenedImage.Names = []string{"<none>"}
		}

		ret = []Image{flattenedImage}
		return ret
	}

	ret = make([]Image, 0, len(image.Names))

	for _, name := range image.Names {
		flattenedImage := *image
		flattenedImage.Names = []string{name}
		ret = append(ret, flattenedImage)
	}

	return ret
}

func (image *Image) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID     string
		Names  []string
		Size   string
		Labels map[string]string
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	image.ID = raw.ID
	image.Names = raw.Names
	image.Size = raw.Size
	image.Labels = raw.Labels
	return nil
}

func (images ImageSlice) Len() int {
	return len(images)
}

func (images ImageSlice) Less(i, j int) bool {
	if len(images[i].Names) != 1 {
		panic("cannot sort unflattened ImageSlice")
	}

	if len(images[j].Names) != 1 {
		panic("cannot sort unflattened ImageSlice")
	}

	return images[i].Names[0] < images[j].Names[0]
}

func (images ImageSlice) Swap(i, j int) {
	images[i], images[j] = images[j], images[i]
}

// CheckVersion compares provided version with the version of Podman.
//
// Takes in one string parameter that should be in the format that is used for versioning (eg. 1.0.0, 2.5.1-dev).
//
// Returns true if the current version is equal to or higher than the required version.
func CheckVersion(requiredVersion string) bool {
	currentVersion, _ := GetVersion()

	currentVersion = version.Normalize(currentVersion)
	requiredVersion = version.Normalize(requiredVersion)

	return version.CompareSimple(currentVersion, requiredVersion) >= 0
}

// ContainerExists checks using Podman if a container with given ID/name exists.
//
// Parameter container is a name or an id of a container.
func ContainerExists(container string) (bool, error) {
	var stdout bytes.Buffer
	args := []string{"-n", "tb", "containers", "ls"}
	err := shell.Run("ctr", nil, &stdout, nil, args...)
	containerCTR := strings.Split(stdout.String(), "\n")
	containerCTR = containerCTR[1 : len(containerCTR)-1]
	for _, ctr := range containerCTR {
		items := strings.Fields(ctr)
		if container == items[0] {
			return true, nil
		}
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

// GetContainers is a wrapper function around `podman ps --format json` command.
//
// Parameter args accepts an array of strings to be passed to the wrapped command (eg. ["-a", "--filter", "123"]).
//
// Returned value is a slice of dynamically unmarshalled json, so it needs to be treated properly.
//
// If a problem happens during execution, first argument is nil and second argument holds the error message.
func GetContainers() ([]map[string]interface{}, error) {

	var stdout bytes.Buffer
	var containers []map[string]interface{}

	args := []string{"-n", "tb", "containers", "ls"}

	if err := shell.Run("ctr", nil, &stdout, nil, args...); err != nil {
		return nil, err
	}
	containerJSONBYTE := convertCtrOutputToJSON(stdout.String())

	if err := json.Unmarshal(containerJSONBYTE, &containers); err != nil {
		return nil, err
	}
	return containers, nil
}

func convertCtrOutputToJSON(ctroutputs string) []byte {
	type fakecontainer struct {
		ID     string
		Names  string
		Status string
		Image  string
		Labels map[string]string
	}
	var containerJSONBYTE []byte
	var stdout bytes.Buffer
	args := []string{"-n", "tb", "task", "ls"}

	shell.Run("ctr", nil, &stdout, nil, args...)
	taskCTR := strings.Split(stdout.String(), "\n")
	taskCTR = taskCTR[1 : len(taskCTR)-1]
	containerCTR := strings.Split(ctroutputs, "\n")
	containerCTR = containerCTR[1 : len(containerCTR)-1]

	for _, ctr := range containerCTR {
		fcon := new(fakecontainer)
		items := strings.Fields(ctr)
		fcon.ID = items[0]
		fcon.Names = items[0]
		fcon.Status = "Created"
		for _, task := range taskCTR {
			titems := strings.Fields(task)
			if fcon.Names == titems[0] {
				fcon.Status = titems[2]
			}
		}
		fcon.Image = items[1]
		fcon.Labels = map[string]string{"com.github.containers.toolbox": "true"}
		var data []byte
		data, _ = json.Marshal(fcon)
		if containerJSONBYTE != nil {
			data = append([]byte(","), data...)
		}
		containerJSONBYTE = append(containerJSONBYTE, data...)
	}
	containerJSONBYTE = append([]byte("["), containerJSONBYTE...)
	containerJSONBYTE = append(containerJSONBYTE, []byte("]")...)
	return containerJSONBYTE
}

// GetImages is a wrapper function around `podman images --format json` command.
//
// Parameter args accepts an array of strings to be passed to the wrapped command (eg. ["-a", "--filter", "123"]).
//
// Returned value is a slice of Images.
//
// If a problem happens during execution, first argument is nil and second argument holds the error message.
func GetImages() ([]Image, error) {
	var stdout bytes.Buffer
	var imageJSONBYTE []byte
	args := []string{"-n", "tb", "images", "ls"}
	if err := shell.Run("ctr", nil, &stdout, nil, args...); err != nil {
		return nil, err
	}
	ctroutputs := string(stdout.Bytes()[:])
	var images []Image
	imageCTR := strings.Split(ctroutputs, "\n")
	imageCTR = imageCTR[:len(imageCTR)-1]
	for index, ctr := range imageCTR {
		if index == 0 {
			continue
		} //skip title column
		fimage := new(Image)
		items := strings.Fields(ctr)
		fimage.ID = items[2]
		name := []string{items[0]}
		fimage.Names = name
		size := items[3] + " " + items[4]
		fimage.Size = size
		fimage.Labels = map[string]string{"com.github.containers.toolbox": "true"}
		var data []byte
		data, _ = json.Marshal(fimage)
		if imageJSONBYTE != nil {
			data = append([]byte(","), data...)
		}
		imageJSONBYTE = append(imageJSONBYTE, data...)
	}
	imageJSONBYTE = append([]byte("["), imageJSONBYTE...)
	imageJSONBYTE = append(imageJSONBYTE, []byte("]")...)

	if err := json.Unmarshal(imageJSONBYTE, &images); err != nil {
		fmt.Println(err)
		return nil, err
	}
	return images, nil
}

// GetVersion returns version of Podman in a string
func GetVersion() (string, error) {
	if podmanVersion != "" {
		return podmanVersion, nil
	}

	var stdout bytes.Buffer

	logLevelString := LogLevel.String()
	args := []string{"--log-level", logLevelString, "version", "--format", "json"}

	if err := shell.Run("podman", nil, &stdout, nil, args...); err != nil {
		return "", err
	}

	output := stdout.Bytes()
	var jsonoutput map[string]interface{}
	if err := json.Unmarshal(output, &jsonoutput); err != nil {
		return "", err
	}

	podmanClientInfoInterface := jsonoutput["Client"]
	switch podmanClientInfo := podmanClientInfoInterface.(type) {
	case nil:
		podmanVersion = jsonoutput["Version"].(string)
	case map[string]interface{}:
		podmanVersion = podmanClientInfo["Version"].(string)
	}
	return podmanVersion, nil
}

// ImageExists checks using Podman if an image with given ID/name exists.
//
// Parameter image is a name or an id of an image.
func ImageExists(image string) (bool, error) {
	var stdout bytes.Buffer
	args := []string{"-n", "tb", "image", "ls"}
	err := shell.Run("ctr", nil, &stdout, nil, args...)
	imageCTR := strings.Split(stdout.String(), "\n")
	imageCTR = imageCTR[1 : len(imageCTR)-1]
	for _, ctr := range imageCTR {
		items := strings.Fields(ctr)
		if image == items[0] {
			return true, nil
		}
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

// Inspect is a wrapper around 'podman inspect' command
//
// Parameter 'typearg' takes in values 'container' or 'image' that is passed to the --type flag
func Inspect(typearg string, target string) (map[string]interface{}, error) {
	var stdout bytes.Buffer

	logLevelString := LogLevel.String()
	args := []string{"--log-level", logLevelString, "inspect", "--format", "json", "--type", typearg, target}

	if err := shell.Run("podman", nil, &stdout, nil, args...); err != nil {
		return nil, err
	}

	output := stdout.Bytes()
	var info []map[string]interface{}

	if err := json.Unmarshal(output, &info); err != nil {
		return nil, err
	}

	return info[0], nil
}

func IsToolboxContainer(container string) (bool, error) {
	info, err := Inspect("container", container)
	if err != nil {
		return false, fmt.Errorf("failed to inspect container %s", container)
	}

	labels, _ := info["Config"].(map[string]interface{})["Labels"].(map[string]interface{})
	if labels["com.github.containers.toolbox"] != "true" && labels["com.github.debarshiray.toolbox"] != "true" {
		return false, fmt.Errorf("%s is not a toolbox container", container)
	}

	return true, nil
}

func IsToolboxImage(image string) (bool, error) {
	info, err := Inspect("image", image)
	if err != nil {
		return false, fmt.Errorf("failed to inspect image %s", image)
	}

	if info["Labels"] == nil {
		return false, fmt.Errorf("%s is not a toolbox image", image)
	}

	labels := info["Labels"].(map[string]interface{})
	if labels["com.github.containers.toolbox"] != "true" && labels["com.github.debarshiray.toolbox"] != "true" {
		return false, fmt.Errorf("%s is not a toolbox image", image)
	}

	return true, nil
}

func Pull(imageName string) error {
	args := []string{"-n", "tb", "image", "pull"}

	args = append(args, imageName)

	if err := shell.Run("ctr", nil, nil, nil, args...); err != nil {
		return err
	}

	return nil
}

func RemoveContainer(container string, forceDelete bool) error {
	logrus.Debugf("Removing container %s", container)
	args := []string{"-n", "tb", "container", "rm"}

	args = append(args, container)

	exitCode, err := shell.RunWithExitCode("ctr", nil, nil, nil, args...)
	switch exitCode {
	case 0:
		if err != nil {
			panic("unexpected error: 'podman rm' finished successfully")
		}
	case 1:
		err = fmt.Errorf("container %s does not exist,or container is running", container)
	default:
		err = fmt.Errorf("failed to remove container %s", container)
	}

	if err != nil {
		return err
	}

	return nil
}

func RemoveImage(image string, forceDelete bool) error {
	logrus.Debugf("Removing image %s", image)

	args := []string{"-n", "tb", "image", "rm"}

	args = append(args, image)

	exitCode, err := shell.RunWithExitCode("ctr", nil, nil, nil, args...)
	//Whether or not the image is succesdfully removed, "ctr i rm " returns 0 as exitcode.
	switch exitCode {
	case 0:
		if err != nil {
			panic("unexpected error: 'podman rmi' finished successfully")
		}
	case 1:
		err = fmt.Errorf("image %s does not exist", image)
	case 2:
		err = fmt.Errorf("image %s has dependent children", image)
	default:
		err = fmt.Errorf("failed to remove image %s", image)
	}

	if err != nil {
		return err
	}

	return nil
}

func SetLogLevel(logLevel logrus.Level) {
	LogLevel = logLevel
}

func Start(container string, stderr io.Writer) error {
	logLevelString := LogLevel.String()
	args := []string{"--log-level", logLevelString, "start", container}

	if err := shell.Run("podman", nil, nil, stderr, args...); err != nil {
		return err
	}

	return nil
}

func SystemMigrate(ociRuntimeRequired string) error {
	logLevelString := LogLevel.String()
	args := []string{"--log-level", logLevelString, "system", "migrate"}
	if ociRuntimeRequired != "" {
		args = append(args, []string{"--new-runtime", ociRuntimeRequired}...)
	}

	if err := shell.Run("podman", nil, nil, nil, args...); err != nil {
		return err
	}

	return nil
}
