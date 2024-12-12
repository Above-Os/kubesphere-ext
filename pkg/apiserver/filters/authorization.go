/*
Copyright 2020 The KubeSphere Authors.

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

package filters

import (
	"context"
	"errors"
	"fmt"
	jwt "github.com/dgrijalva/jwt-go"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	"k8s.io/klog"

	autht "kubesphere.io/kubesphere/pkg/apiserver/authentication/token"
	"kubesphere.io/kubesphere/pkg/apiserver/authorization/authorizer"
	"kubesphere.io/kubesphere/pkg/apiserver/request"
	"net/http"
)

// WithAuthorization passes all authorized requests on to handler, and returns forbidden error otherwise.
func WithAuthorization(handler http.Handler, authorizers authorizer.Authorizer) http.Handler {
	if authorizers == nil {
		klog.V(0).Infof("Authorization is disabled")
		return handler
	}

	defaultSerializer := serializer.NewCodecFactory(runtime.NewScheme()).WithoutConversion()

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()

		attributes, err := getAuthorizerAttributes(ctx)
		if err != nil {
			responsewriters.InternalError(w, req, err)
		}
		klog.V(0).Infof("userinfo.username: %v", attributes.GetUser())
		klog.V(0).Infof("userinfo.path: %v", attributes.GetPath())

		authorized, reason, err := authorizers.Authorize(attributes)
		if authorized == authorizer.DecisionAllow {
			handler.ServeHTTP(w, req)
			return
		}

		if err != nil {
			responsewriters.InternalError(w, req, err)
			return
		}

		klog.V(0).Infof("Forbidden: %#v, Reason: %q", req.RequestURI, reason)
		responsewriters.Forbidden(ctx, attributes, w, req, reason, defaultSerializer)
	})
}

func getAuthorizerAttributes(ctx context.Context) (authorizer.Attributes, error) {
	attribs := authorizer.AttributesRecord{}

	user, ok := request.UserFrom(ctx)
	if ok {
		attribs.User = user
	}

	requestInfo, found := request.RequestInfoFrom(ctx)
	if !found {
		return nil, errors.New("no RequestInfo found in the context")
	}

	// Start with common attributes that apply to resource and non-resource requests
	attribs.ResourceScope = requestInfo.ResourceScope
	attribs.ResourceRequest = requestInfo.IsResourceRequest
	attribs.Path = requestInfo.Path
	attribs.Verb = requestInfo.Verb
	attribs.KubernetesRequest = requestInfo.IsKubernetesRequest
	attribs.APIGroup = requestInfo.APIGroup
	attribs.APIVersion = requestInfo.APIVersion
	attribs.Resource = requestInfo.Resource
	attribs.Subresource = requestInfo.Subresource
	attribs.Namespace = requestInfo.Namespace
	attribs.Name = requestInfo.Name

	return &attribs, nil
}

// if using basic auth. But only treats request with requestURI `/oauth/authorize` as login attempt
func WithAuthentication(handler http.Handler, authRequest authenticator.Request) http.Handler {
	if authRequest == nil {
		klog.Warningf("Authentication is disabled")
		return handler
	}
	s := serializer.NewCodecFactory(runtime.NewScheme()).WithoutConversion()

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {

		klog.V(0).Infof("withAuthentication: path: %v", req.URL.Path)

		resp, ok, err := authRequest.AuthenticateRequest(req)
		_, _, usingBasicAuth := req.BasicAuth()

		defer func() {
			// if we authenticated successfully, go ahead and remove the bearer token so that no one
			// is ever tempted to use it inside of the API server
			if usingBasicAuth && ok {
				req.Header.Del("Authorization")
			}
		}()

		if err != nil || !ok {
			ctx := req.Context()
			requestInfo, found := request.RequestInfoFrom(ctx)
			if !found {
				responsewriters.InternalError(w, req, errors.New("no RequestInfo found in the context"))
				return
			}
			gv := schema.GroupVersion{Group: requestInfo.APIGroup, Version: requestInfo.APIVersion}
			responsewriters.ErrorNegotiated(apierrors.NewUnauthorized(fmt.Sprintf("Unauthorized: %s", err)), s, gv, w, req)
			return
		}

		klog.V(0).Infof("userInfo: %#v", resp.User)

		req = req.WithContext(request.WithUser(req.Context(), resp.User))
		handler.ServeHTTP(w, req)
	})
}

// ValidateToken validates a token by performing an authentication check.
func ValidateToken(ctx context.Context, tokenString string) (string, error) {
	token, err := jwt.ParseWithClaims(tokenString, &autht.Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}

		return []byte("REPLACE_WITH_RANDOM"), nil
	})

	if err != nil {
		return "", err
	}

	if claims, ok := token.Claims.(*autht.Claims); ok && token.Valid {
		klog.V(0).Infof("claims: %#v", claims)
		return claims.Username, nil
	}
	return "", fmt.Errorf("invalid token, or claims not match")
}
