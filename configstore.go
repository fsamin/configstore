package configstore

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
)

const (
	// ConfigEnvVar defines the environment variable used to set up the configuration providers via InitFromEnvironment
	ConfigEnvVar = "CONFIGURATION_FROM"
)

type Store struct {
	providers             map[string]Provider
	pMut                  sync.Mutex
	allowProviderOverride bool
	watchers              []chan struct{}
	watchersMut           sync.Mutex
}

var (
	_store            *Store
	pFactMut          sync.Mutex
	providerFactories                                          = map[string]func(string){}
	LogInfo           func(format string, args ...interface{}) = log.Printf
)

func init() {
	_store = New()
	RegisterProviderFactory("file", File)
	RegisterProviderFactory("filelist", FileList)
	RegisterProviderFactory("filetree", FileTree)
}

func New() *Store {
	s := Store{
		pMut: sync.Mutex{},
	}
	return s.Clear()
}

func Get() *Store {
	return _store
}

func (s *Store) Clear() *Store {
	s.pMut.Lock()
	defer s.pMut.Unlock()

	s.providers = map[string]Provider{}
	s.allowProviderOverride = false
	return s
}

// A Provider retrieves config items and makes them available to the configstore,
// Their implementations can vary wildly (HTTP API, file, env, hardcoded test, ...)
// and their results will get merged by the configstore library.
// It's the responsability of the application using configstore to register suitable providers.
type Provider func() (ItemList, error)

type IProvider interface {
	Items() (ItemList, error)
}

// RegisterProvider registers a provider
func RegisterProvider(name string, f Provider) {
	_store.RegisterProvider(name, f)
}

func Register(name string, f IProvider) {
	_store.Register(name, f)
}

// Register registers a provider
func (s *Store) Register(name string, f IProvider) {
	s.RegisterProvider(name, f.Items)
}

// RegisterProvider registers a provider
func (s *Store) RegisterProvider(name string, f Provider) {
	s.pMut.Lock()
	defer s.pMut.Unlock()
	_, ok := s.providers[name]
	if ok && !s.allowProviderOverride {
		panic(fmt.Sprintf("conflict on configuration provider: %s", name))
	}
	s.providers[name] = f
}

// AllowProviderOverride allows multiple calls to RegisterProvider() with the same provider name.
// This is useful for controlled test cases, but is not recommended in the context of a real
// application.
func AllowProviderOverride() {
	_store.AllowProviderOverride()
}

// AllowProviderOverride allows multiple calls to RegisterProvider() with the same provider name.
// This is useful for controlled test cases, but is not recommended in the context of a real
// application.
func (s *Store) AllowProviderOverride() {
	LogInfo("configstore: ATTENTION: PROVIDER OVERRIDE ALLOWED/ENABLED")
	s.pMut.Lock()
	defer s.pMut.Unlock()
	s.allowProviderOverride = true
}

// RegisterProviderFactory registers a factory function so that InitFromEnvironment can properly
// instantiate configuration providers via name + argument.
func RegisterProviderFactory(name string, f func(string)) {
	pFactMut.Lock()
	defer pFactMut.Unlock()
	_, ok := providerFactories[name]
	if ok {
		panic(fmt.Sprintf("conflict on configuration provider factory: %s", name))
	}
	providerFactories[name] = f
}

// InitFromEnvironment initializes configuration providers via their name and an optional argument.
// Suitable provider factories should have been registered via RegisterProviderFactory for this to work.
// Built-in providers (File, FileList, FileTree, ...) are registered by default.
//
// Valid example:
// CONFIGURATION_FROM=file:/etc/myfile.conf,file:/etc/myfile2.conf,filelist:/home/foobar/configs
func InitFromEnvironment() {
	_store.InitFromEnvironment()
}

// InitFromEnvironment initializes configuration providers via their name and an optional argument.
// Suitable provider factories should have been registered via RegisterProviderFactory for this to work.
// Built-in providers (File, FileList, FileTree, ...) are registered by default.
//
// Valid example:
// CONFIGURATION_FROM=file:/etc/myfile.conf,file:/etc/myfile2.conf,filelist:/home/foobar/configs
func (s *Store) InitFromEnvironment() {
	pFactMut.Lock()
	defer pFactMut.Unlock()

	cfg := os.Getenv(ConfigEnvVar)
	if cfg == "" {
		return
	}
	cfgList := strings.Split(cfg, ",")
	for _, c := range cfgList {
		parts := strings.SplitN(c, ":", 2)
		name := c
		arg := ""
		if len(parts) > 1 {
			name = parts[0]
			arg = parts[1]
		}
		name = strings.TrimSpace(name)
		arg = strings.TrimSpace(arg)
		f := providerFactories[name]
		if f == nil {
			s.ErrorProvider(fmt.Sprintf("%s:%s", name, arg), errors.New("failed to instantiate provider factory"))
		} else {
			f(arg)
		}
	}
}

// Watch returns a channel which you can range over.
// You will get unblocked every time a provider notifies of a configuration change.
func Watch() <-chan struct{} {
	return _store.Watch()
}

// Watch returns a channel which you can range over.
// You will get unblocked every time a provider notifies of a configuration change.
func (s *Store) Watch() <-chan struct{} {
	// buffer size == 1, notifications will never use a blocking write
	newCh := make(chan struct{}, 1)
	s.watchersMut.Lock()
	s.watchers = append(s.watchers, newCh)
	s.watchersMut.Unlock()
	return newCh
}

// NotifyWatchers is used by providers to notify of configuration changes.
// It unblocks all the watchers which are ranging over a watch channel.
func NotifyWatchers() {
	_store.NotifyWatchers()
}

// NotifyWatchers is used by providers to notify of configuration changes.
// It unblocks all the watchers which are ranging over a watch channel.
func (s *Store) NotifyWatchers() {
	s.watchersMut.Lock()
	for _, ch := range s.watchers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	s.watchersMut.Unlock()
}
