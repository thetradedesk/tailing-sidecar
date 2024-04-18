package main

import (
	"C"
	"fmt"
	"path/filepath"
	"unsafe"

	"github.com/fluent/fluent-bit-go/output"
)

//export FLBPluginRegister
func FLBPluginRegister(def unsafe.Pointer) int {
	return output.FLBPluginRegister(def, "gstdout", "Stdout GO!")
}

//export FLBPluginInit
// (fluentbit will call this)
// plugin (context) pointer to fluentbit context (state/ c code)
func FLBPluginInit(plugin unsafe.Pointer) int {
	// Example to retrieve an optional configuration parameter
	// param := output.FLBPluginConfigKey(plugin, "param")
	// fmt.Printf("[flb-go] plugin parameter = '%s'\n", param)
	return output.FLB_OK
}

//export FLBPluginFlush
func FLBPluginFlush(data unsafe.Pointer, length C.int, tag *C.char) int {
	var ret int
	var record map[interface{}]interface{}

	// Create Fluent Bit decoder
	dec := output.NewDecoder(data, int(length))

	// Iterate Records
	for {
		// Extract Record
		ret, _, record = output.GetRecord(dec)
		if ret != 0 {
			break
		}

		// Print logs in custom format
		filenameBytes, ok1 := record["filename"].([]uint8)
		logBytes, ok2 := record["log"].([]uint8)
		if ok1 && ok2 {
			filename := filepath.Base(string(filenameBytes))
			logMessage := string(logBytes)
			fmt.Printf("%s: %s\n", filename, logMessage)
		} else {
			fmt.Println("Error: Unable to retrieve 'filename' or 'log' from record")
		}
	}

	// Return options:
	//
	// output.FLB_OK    = data have been processed.
	// output.FLB_ERROR = unrecoverable error, do not try this again.
	// output.FLB_RETRY = retry to flush later.
	return output.FLB_OK
}

//export FLBPluginExit
func FLBPluginExit() int {
	return output.FLB_OK
}

func main() {
}
