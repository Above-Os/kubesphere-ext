package lldap

import (
	"context"
	"fmt"
	jwt "github.com/dgrijalva/jwt-go"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/user"
	corev1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog"
	autht "kubesphere.io/kubesphere/pkg/apiserver/authentication/token"
)

type jwtAuthInterface interface {
	AuthenticateToken(ctx context.Context, token string) (*authenticator.Response, bool, error)
}

type jwtAuthenticator struct {
	secretLister corev1.SecretLister
}

func NewJwtAuthenticator(secretLister corev1.SecretLister) jwtAuthInterface {
	return &jwtAuthenticator{
		secretLister: secretLister,
	}
}

func (j *jwtAuthenticator) AuthenticateToken(ctx context.Context, tokenString string) (*authenticator.Response, bool, error) {
	token, err := jwt.ParseWithClaims(tokenString, &autht.Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		secert, err := j.secretLister.Secrets("os-system").Get("lldap-credentials")
		if err != nil {
			return nil, err
		}
		jwtSecretKey := secert.Data["lldap-jwt-secret"]
		klog.V(0).Infof("jwtSecretKey: %s", string(jwtSecretKey))
		return jwtSecretKey, nil
		//return []byte("REPLACE_WITH_RANDOM"), nil
	})

	if err != nil {
		return nil, false, err
	}

	if claims, ok := token.Claims.(*autht.Claims); ok && token.Valid {
		klog.V(0).Infof("claims: %#v", claims)
		return &authenticator.Response{
			User: &user.DefaultInfo{
				Name: claims.Username,
				UID:  claims.Username,
			},
		}, true, nil
	}
	return nil, false, fmt.Errorf("invalid token, or claims not match")

}
