package lldap

import (
	"context"
	"errors"
	"github.com/Khan/genqlient/graphql"
	"github.com/thoas/go-funk"

	"fmt"
	"github.com/go-resty/resty/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
	"kubesphere.io/api/iam/v1alpha2"
	"net/http"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

type authedTransport struct {
	key     string
	wrapped http.RoundTripper
}

func (t *authedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.key)
	return t.wrapped.RoundTrip(req)
}

func createGraphClient(url, token string) graphql.Client {
	httpClient := http.Client{
		Transport: &authedTransport{
			key:     token,
			wrapped: http.DefaultTransport,
		},
	}
	return graphql.NewClient(url+"/api/graphql", &httpClient)
}

type LLdapOperator struct {
	*v1alpha2.LLdapProvider
	client.Client
	restClient *resty.Client
}

func New(lldap *v1alpha2.LLdapProvider) *LLdapOperator {
	return &LLdapOperator{
		LLdapProvider: lldap,
		restClient:    resty.New().SetTimeout(5 * time.Second),
	}
}

func (l *LLdapOperator) getCredentialVal(key string) (string, error) {
	klog.Infof("credentialSecret: %#v", *l.CredentialsSecret)
	if l.CredentialsSecret != nil {
		name := types.NamespacedName{Namespace: l.CredentialsSecret.Namespace, Name: l.CredentialsSecret.Name}
		var secret corev1.Secret
		klog.Infof("l.Client is nil: %v", l.Client == nil)
		err := l.Client.Get(context.TODO(), name, &secret)
		if err != nil {
			klog.Infof(".......%v", err)
			return "", err
		}
		if value, ok := secret.Data[key]; ok {
			return string(value), nil
		}
	}
	return "", fmt.Errorf("can not find credentialval for key %s", key)
}

func (l *LLdapOperator) getToken() (string, error) {
	username, err := l.getCredentialVal("lldap-ldap-user-dn")
	if err != nil {
		return "", err
	}
	password, err := l.getCredentialVal("lldap-ldap-user-pass")
	if err != nil {
		return "", err
	}
	resp, err := login(l.URL, username, password)
	if err != nil {
		return "", err
	}
	return resp.Token, nil
}

func (l *LLdapOperator) GetUser(name string) (*User, error) {
	token, err := l.getToken()
	if err != nil {
		return nil, err
	}
	graphqlClient := createGraphClient(l.URL, token)
	var viewerUser *GetUserDetailsResponse
	viewerUser, err = GetUserDetails(context.TODO(), graphqlClient, name)
	if err != nil {
		return nil, err
	}
	groups := make([]string, 0)
	for _, g := range viewerUser.User.Groups {
		groups = append(groups, g.DisplayName)
	}

	user := &User{
		Id:          viewerUser.User.Id,
		DisplayName: viewerUser.User.DisplayName,
		Groups:      groups,
	}
	return user, nil
}

func (l *LLdapOperator) GetUserList() ([]User, error) {
	token, err := l.getToken()
	if err != nil {
		return nil, err
	}
	graphqlClient := createGraphClient(l.URL, token)

	var viewerGroupResp *GetGroupListResponse
	viewerGroupResp, err = GetGroupList(context.Background(), graphqlClient)

	if err != nil {
		return nil, err
	}
	users := l.filter(viewerGroupResp.Groups)
	return users, nil
}

func (l *LLdapOperator) filter(groups []GetGroupListGroupsGroup) []User {
	users := make([]User, 0)
	userMap := make(map[string]User)
	klog.Infof("filter: group:%v, user:%v", l.GroupWhitelist, l.UserBlacklist)
	for _, group := range groups {
		if len(l.GroupWhitelist) != 0 && !funk.Contains(l.GroupWhitelist, group.DisplayName) {
			continue
		}
		for _, user := range group.Users {
			if len(l.UserBlacklist) != 0 && funk.Contains(l.UserBlacklist, user.Id) {
				continue
			}
			if v, ok := userMap[user.Id]; ok {
				v.Groups = append(v.Groups, group.DisplayName)
			} else {
				userMap[user.Id] = User{
					Id:          user.Id,
					Email:       user.Email,
					DisplayName: user.DisplayName,
					Groups:      []string{group.DisplayName},
				}
			}
		}
	}

	for _, user := range userMap {
		users = append(users, user)
	}
	return users
}
func (l *LLdapOperator) CreateUser(id, email, displayName, password string, groupID int) error {
	token, err := l.getToken()
	if err != nil {
		return err
	}
	graphqlClient := createGraphClient(l.URL, token)

	//var userResp *GetUserDetailsResponse
	//userResp, err = GetUserDetails(context.TODO(), graphqlClient, id)
	//if err == nil {
	//} else {
	//
	//}

	u := CreateUserInput{
		Id:          id,
		Email:       email,
		DisplayName: displayName,
	}

	var _ *CreateUserResponse
	_, err = CreateUser(context.TODO(), graphqlClient, u)
	if err != nil {
		return err
	}
	err = l.resetPassword(id, password, token)
	if err != nil {
		return err
	}

	var viewerJoinGroupResp *AddUserToGroupResponse
	// lldap_admin group id equal 1
	viewerJoinGroupResp, err = AddUserToGroup(context.TODO(), graphqlClient, id, groupID)
	if err != nil {
		return err
	}
	if viewerJoinGroupResp.AddUserToGroup.Ok == false {
		return fmt.Errorf("user with uid=%d add to group with gid=%d failed", id, 1)
	}

	return nil
}

type resetPassword struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (l *LLdapOperator) resetPassword(username, password, token string) error {
	creds := resetPassword{
		Username: username,
		Password: password,
	}
	url := fmt.Sprintf("%s/auth/simple/register", l.URL)
	client := resty.New()
	resp, err := client.SetTimeout(5*time.Second).R().
		SetHeader("Content-Type", "application/json").
		SetHeader("Authorization", "Bearer "+token).
		SetBody(creds).Post(url)
	if err != nil {
		return err
	}
	if resp.StatusCode() != http.StatusOK {
		return errors.New(resp.String())
	}
	return nil
}
