// Copyright 2016 Amazon.com, Inc. or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
//	http://aws.amazon.com/apache2.0/
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

package api

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecr"
	"github.com/aws/aws-sdk-go/service/ecr/ecriface"
	"github.com/awslabs/amazon-ecr-credential-helper/ecr-login/cache"
	log "github.com/cihub/seelog"
)

const proxyEndpointScheme = "https://"

type Client interface {
	GetCredentials(registry, image string) (string, string, error)
}
type defaultClient struct {
	ecrClient       ecriface.ECRAPI
	credentialCache cache.CredentialsCache
}

func (self *defaultClient) GetCredentials(registry, image string) (string, string, error) {
	log.Debugf("GetCredentials for %s", registry)

	cachedEntry := self.credentialCache.Get(registry)

	if cachedEntry != nil {
		if cachedEntry.IsValid(time.Now()) {
			log.Debugf("Using cached token for %s", registry)
			return extractToken(cachedEntry.AuthorizationToken)
		} else {
			log.Debugf("Cached token is no longer valid. RequestAt: %s, ExpiresAt: %s", cachedEntry.RequestedAt, cachedEntry.ExpiresAt)
		}
	}

	log.Debugf("Calling ECR.GetAuthorizationToken for %s", registry)

	input := &ecr.GetAuthorizationTokenInput{
		RegistryIds: []*string{aws.String(registry)},
	}

	output, err := self.ecrClient.GetAuthorizationToken(input)

	if err != nil || output == nil {
		if err == nil {
			err = fmt.Errorf("Missing AuthorizationData in ECR response for %s", registry)
		}

		// if we have a cached token, fall back to avoid failing the request. This may result an expired token
		// being returned, but if there is a 500 or timeout from the service side, we'd like to attempt to re-use an
		// old token. We invalidate tokens prior to their expiration date to help mitigate this scenario.
		if cachedEntry != nil {
			log.Infof("Got error fetching authorization token. Falling back to cached token. Error was: %s", err)
			return extractToken(cachedEntry.AuthorizationToken)
		}

		return "", "", err
	}
	for _, authData := range output.AuthorizationData {
		if authData.ProxyEndpoint != nil &&
			strings.HasPrefix(proxyEndpointScheme+image, aws.StringValue(authData.ProxyEndpoint)) &&
			authData.AuthorizationToken != nil {
			authEntry := cache.AuthEntry{
				AuthorizationToken: aws.StringValue(authData.AuthorizationToken),
				RequestedAt:        time.Now(),
				ExpiresAt:          aws.TimeValue(authData.ExpiresAt),
				ProxyEndpoint:      aws.StringValue(authData.ProxyEndpoint),
			}

			self.credentialCache.Set(registry, &authEntry)
			return extractToken(aws.StringValue(authData.AuthorizationToken))
		}
	}
	return "", "", fmt.Errorf("No AuthorizationToken found for %s", registry)
}

func extractToken(token string) (string, string, error) {
	decodedToken, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "", "", err
	}
	parts := strings.SplitN(string(decodedToken), ":", 2)
	return parts[0], parts[1], nil
}
