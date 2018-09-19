// +build windows
/********************************************************************************
 * Copyright (C) 2018 Kichwa Coders
 *
 * This program and the accompanying materials are made available under the
 * terms of the Eclipse Public License v. 2.0 which is available at
 * http://www.eclipse.org/legal/epl-2.0.
 *
 * This Source Code may also be made available under the following Secondary
 * Licenses when the conditions for such availability set forth in the Eclipse
 * Public License v. 2.0 are satisfied: GNU General Public License, version 2
 * with the GNU Classpath Exception which is available at
 * https://www.gnu.org/software/classpath/license.html.
 *
 * SPDX-License-Identifier: EPL-2.0 OR GPL-2.0 WITH Classpath-exception-2.0
 ********************************************************************************/

package main

import (
	"fmt"
	"log"
	"net/url"

	toast "gopkg.in/toast.v1"
)

// PermissionDeniedPrompt display a notification to the user that the origin was denied
func PermissionDeniedPrompt(remote string) {
	u, err := url.Parse(fmt.Sprintf(`http://localhost:%s/help`, port))
	if err != nil {
		panic("Failed to parse?")
	}
	parameters := url.Values{}
	parameters.Add("origin", remote)
	u.RawQuery = parameters.Encode()

	notification := toast.Notification{
		AppID:   "{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\\WindowsPowerShell\\v1.0\\powershell.exe",
		Title:   "USB Debug Connection Denied",
		Message: "A USB debug connection has been initiated from " + remote + " which is not in the allowed list and therefore the debug session was denied.",
		Actions: []toast.Action{
			{Type: "protocol", Label: "Help", Arguments: u.String()},
		},
	}
	err = notification.Push()
	if err != nil {
		log.Fatalln(err)
	}
}

// PermissionAllowedPrompt display a notification to the user that the origin was allowed
func PermissionAllowedPrompt(remote string) {
	notification := toast.Notification{
		AppID:   "{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\\WindowsPowerShell\\v1.0\\powershell.exe",
		Title:   "USB Debug Connection Allowed",
		Message: "A USB debug connection has been initiated from " + remote + " which is in the allowed list and therefore has been allowed.",
		Actions: []toast.Action{},
	}
	err := notification.Push()
	if err != nil {
		log.Fatalln(err)
	}
}
