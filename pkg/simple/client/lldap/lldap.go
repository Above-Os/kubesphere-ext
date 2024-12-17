package lldap

import (
	iamv1alpha2 "kubesphere.io/api/iam/v1alpha2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type LLdapInterface interface {
	GetUser(name string) (*User, error)
	GetUserList() ([]User, error)
}

type LLdap struct {
	Name string
	client.Client
	UserSync *iamv1alpha2.Sync
	Provider *iamv1alpha2.LLdapProvider
}

func NewLLdap(client client.Client, sync *iamv1alpha2.Sync, provider *iamv1alpha2.LLdapProvider) *LLdap {
	return &LLdap{
		Name:     "lldap",
		Client:   client,
		UserSync: sync,
		Provider: provider,
	}
}

func (l *LLdap) Sync() ([]User, error) {
	op := New(l.Provider)
	if op.Client == nil {
		op.Client = l.Client
	}
	return op.GetUserList()
}

func (l *LLdap) GetProviderName() string {
	return l.Name
}
