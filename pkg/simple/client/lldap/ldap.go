package lldap

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Khan/genqlient/graphql"
	"github.com/thoas/go-funk"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	iamv1alpha2 "kubesphere.io/api/iam/v1alpha2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Ldap struct {
	Name string
	client.Client
	UserSync *iamv1alpha2.Sync
	Provider *iamv1alpha2.LdapProvider
}

type authedTransport struct {
	key     string
	wrapped http.RoundTripper
}

func (t *authedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.key)
	return t.wrapped.RoundTrip(req)
}

var graphqlClient graphql.Client

func createGraphClient(url, token string) {
	httpClient := http.Client{
		Transport: &authedTransport{
			key:     token,
			wrapped: http.DefaultTransport,
		},
	}
	graphqlClient = graphql.NewClient(url+"/api/graphql", &httpClient)
}

func (l *Ldap) getCredentialVal(key string) (string, error) {
	klog.Infof("credentialSecret: %#v", *l.Provider.CredentialsSecret)
	if l.Provider != nil && l.Provider.CredentialsSecret != nil {
		name := types.NamespacedName{Namespace: l.Provider.CredentialsSecret.Namespace, Name: l.Provider.CredentialsSecret.Name}
		var secret corev1.Secret
		err := l.Client.Get(context.TODO(), name, &secret)
		if err != nil {
			klog.Infof(".......%v", err)
			return "", err
		}
		if value, ok := secret.Data[key]; ok {
			return string(value), nil
		}
	}
	return "", errors.New("d")
}

func (l *Ldap) Sync() ([]User, error) {

	klog.Infof("lllllll: %v", l)
	username, err := l.getCredentialVal("lldap-ldap-user-dn")
	if err != nil {
		klog.Infof("Sync: getcre, err=%v", err)
		return nil, err
	}
	ctrl.Log.Info("Sync: xxxx", "err", err)
	password, err := l.getCredentialVal("lldap-ldap-user-pass")
	if err != nil {
		return nil, err
	}
	klog.Infof("Sync: username:%s, password:%s\n", username, password)

	// bind and then sync ldap user
	res, err := login(l.Provider.URL, username, password)
	if err != nil {
		fmt.Println("login:", err)
		return nil, err
	}
	fmt.Printf("%#v\n", res)
	createGraphClient(l.Provider.URL, res.Token)

	var viewerGroupResp *GetGroupListResponse
	viewerGroupResp, err = GetGroupList(context.Background(), graphqlClient)
	if err != nil {
		fmt.Println("get group list:", err)
		return nil, err
	}
	fmt.Printf("GROUP:%#v\n", viewerGroupResp)
	//return viewerGroupResp, nil

	users := l.filter(viewerGroupResp.Groups)

	return users, nil
}

func (l *Ldap) GetProviderName() string {
	return l.Name
}

func (l *Ldap) filter(groups []GetGroupListGroupsGroup) []User {
	users := make([]User, 0)
	userMap := make(map[string]User)
	klog.Infof("filter: group:%v, user:%v", l.Provider.GroupWhitelist, l.Provider.UserBlacklist)
	for _, group := range groups {
		if len(l.Provider.GroupWhitelist) != 0 && !funk.Contains(l.Provider.GroupWhitelist, group.DisplayName) {
			continue
		}
		for _, user := range group.Users {
			if len(l.Provider.UserBlacklist) != 0 && funk.Contains(l.Provider.UserBlacklist, user.Id) {
				continue
			}
			if v, ok := userMap[user.Id]; ok {
				v.GroupName = append(v.GroupName, group.DisplayName)
			} else {
				userMap[user.Id] = User{
					Id:          user.Id,
					DisplayName: user.DisplayName,
					GroupName:   []string{group.DisplayName},
				}
			}
		}
	}

	for _, user := range userMap {
		users = append(users, user)
	}
	return users
}
