package configstore

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ghodss/yaml"
)

// ErrorProvider registers a configstore provider which always returns an error.
func (s *Store) ErrorProvider(name string, err error) {
	s.RegisterProvider(name, func() (ItemList, error) { return ItemList{}, err })
}

// File registers a configstore provider which reads from the file given in parameter (static content).
func File(filename string) {
	_store.file(filename, false, nil)
}

// FileRefresh registers a configstore provider which readfs from the file given in parameter (provider watches file stat for auto refresh, watchers get notified).
func FileRefresh(filename string) {
	_store.file(filename, true, nil)
}

// FileCustom registers a configstore provider which reads from the file given in parameter, and loads the content using the given unmarshal function
func FileCustom(filename string, fn func([]byte) ([]Item, error)) {
	_store.file(filename, false, fn)
}

// FileCustomRefresh registers a configstore provider which reads from the file given in parameter, and loads the content using the given unmarshal function; and watches file stat for auto refresh
func FileCustomRefresh(filename string, fn func([]byte) ([]Item, error)) {
	_store.file(filename, true, fn)
}

func (s *Store) File(filename string) {
	s.file(filename, false, nil)
}

// FileRefresh registers a configstore provider which readfs from the file given in parameter (provider watches file stat for auto refresh, watchers get notified).
func (s *Store) FileRefresh(filename string) {
	s.file(filename, true, nil)
}

// FileCustom registers a configstore provider which reads from the file given in parameter, and loads the content using the given unmarshal function
func (s *Store) FileCustom(filename string, fn func([]byte) ([]Item, error)) {
	s.file(filename, false, fn)
}

// FileCustomRefresh registers a configstore provider which reads from the file given in parameter, and loads the content using the given unmarshal function; and watches file stat for auto refresh
func (s *Store) FileCustomRefresh(filename string, fn func([]byte) ([]Item, error)) {
	s.file(filename, true, fn)
}

func (s *Store) file(filename string, refresh bool, fn func([]byte) ([]Item, error)) {
	if filename == "" {
		return
	}

	providername := fmt.Sprintf("file:%s", filename)

	last := time.Now()
	vals, err := readFile(filename, fn)
	if err != nil {
		s.ErrorProvider(providername, err)
		return
	}
	inmem := InMemory(providername)
	LogInfo("Configuration from file: %s", filename)
	inmem.Add(vals...)

	if refresh {
		go func() {
			ticker := time.NewTicker(10 * time.Second)
			for range ticker.C {
				finfo, err := os.Stat(filename)
				if err != nil {
					continue
				}
				if finfo.ModTime().After(last) {
					last = finfo.ModTime()
				} else {
					continue
				}
				vals, err := readFile(filename, fn)
				if err != nil {
					continue
				}
				inmem.mut.Lock()
				inmem.items = vals
				inmem.mut.Unlock()
				s.NotifyWatchers()
			}
		}()
	}
}

// FileTree registers a configstore provider which reads from the files contained in the directory given in parameter.
// A limited hierarchy is supported: files can either be top level (in which case the file name will be used as the item key),
// or nested in a single sub-directory (in which case the sub-directory name will be used as item key for all the files contained in it).
// The content of the files should be the plain data, with no envelope.
// Capitalization can be used to indicate item priority for sub-directories containing multiple items which should be differentiated.
// Capitalized = higher priority.
func FileTree(dirname string) {
	_store.FileTree(dirname)
}

// FileTree registers a configstore provider which reads from the files contained in the directory given in parameter.
// A limited hierarchy is supported: files can either be top level (in which case the file name will be used as the item key),
// or nested in a single sub-directory (in which case the sub-directory name will be used as item key for all the files contained in it).
// The content of the files should be the plain data, with no envelope.
// Capitalization can be used to indicate item priority for sub-directories containing multiple items which should be differentiated.
// Capitalized = higher priority.
func (s *Store) FileTree(dirname string) {
	if dirname == "" {
		return
	}

	providername := fmt.Sprintf("filetree:%s", dirname)

	files, err := ioutil.ReadDir(dirname)
	if err != nil {
		s.ErrorProvider(providername, err)
		return
	}

	items := []Item{}

	for _, f := range files {
		filename := filepath.Join(dirname, f.Name())

		if f.IsDir() {
			items, err = browseDir(items, filename, f.Name())
			if err != nil {
				s.ErrorProvider(providername, err)
				return
			}
		} else {
			it, err := readItem(filename, f.Name(), f.Name())
			if err != nil {
				s.ErrorProvider(providername, err)
				return
			}
			items = append(items, it)
		}
	}

	inmem := InMemory(providername)
	for _, it := range items {
		inmem.Add(it)
	}
}

// FileList registers a configstore provider which reads from the files contained in the directory given in parameter.
// The content of the files should be JSON/YAML similar to the File provider.
func FileList(dirname string) {
	_store.FileList(dirname)
}

// FileList registers a configstore provider which reads from the files contained in the directory given in parameter.
// The content of the files should be JSON/YAML similar to the File provider.
func (s *Store) FileList(dirname string) {
	if dirname == "" {
		return
	}

	files, err := ioutil.ReadDir(dirname)
	if err != nil {
		s.ErrorProvider(fmt.Sprintf("filelist:%s", dirname), err)
		return
	}

	for _, file := range files {
		s.File(filepath.Join(dirname, file.Name()))
	}
}

func browseDir(items []Item, path, basename string) ([]Item, error) {

	files, err := ioutil.ReadDir(path)
	if err != nil {
		return items, err
	}

	for _, f := range files {
		filename := filepath.Join(path, f.Name())
		if f.IsDir() {
			return items, fmt.Errorf("subdir %s: encountered nested directory %s, max 1 level of nesting", basename, f.Name())
		}
		it, err := readItem(filename, f.Name(), basename)
		if err != nil {
			return items, err
		}
		items = append(items, it)
	}

	return items, nil
}

func readItem(path, basename, itemKey string) (Item, error) {
	content, err := ioutil.ReadFile(path)
	if err != nil {
		return Item{}, err
	}
	priority := int64(5)
	first, _ := utf8.DecodeRuneInString(basename)
	if unicode.IsUpper(first) {
		priority = 10
	}
	return NewItem(itemKey, string(content), priority), nil
}

func readFile(filename string, fn func([]byte) ([]Item, error)) ([]Item, error) {
	vals := []Item{}
	b, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	if fn != nil {
		return fn(b)
	}
	err = yaml.Unmarshal(b, &vals)
	if err != nil {
		return nil, err
	}
	return vals, nil
}

// InMemoryProvider implements an in-memory configstore provider.
type InMemoryProvider struct {
	items []Item
	mut   sync.Mutex
}

// Add appends an item to the in-memory list.
func (inmem *InMemoryProvider) Add(s ...Item) *InMemoryProvider {
	inmem.mut.Lock()
	defer inmem.mut.Unlock()
	inmem.items = append(inmem.items, s...)
	return inmem
}

// Items returns the in-memory item list. This is the function that gets called by configstore.
func (inmem *InMemoryProvider) Items() (ItemList, error) {
	inmem.mut.Lock()
	defer inmem.mut.Unlock()
	return ItemList{Items: inmem.items}, nil
}

// InMemory registers an InMemoryProvider with a given arbitrary name and returns it.
// You can append any number of items to it, see Add().
func InMemory(name string) *InMemoryProvider {
	return _store.InMemory(name)
}

func (s *Store) InMemory(name string) *InMemoryProvider {
	inmem := &InMemoryProvider{}
	s.RegisterProvider(name, inmem.Items)
	return inmem
}

func EnvVariable(opts ...EnvVariableOptions) *EnvVariableProvider {
	return _store.EnvVariable(opts...)
}

type EnvVariableProvider struct {
	inMemory InMemoryProvider
	bindings map[string]string
	priority int64
}

type EnvVariableOptions func(s *EnvVariableProvider)

func WithPriority(p int64) EnvVariableOptions {
	return func(s *EnvVariableProvider) {
		s.priority = p
	}
}

func WithAutomaticBinding(prefix, keySeparator string) EnvVariableOptions {
	return func(s *EnvVariableProvider) {
		environ := os.Environ()
		for _, env := range environ {
			splittedEnv := strings.SplitN(env, "=", 2)
			variable := splittedEnv[0]
			if !strings.HasPrefix(variable, prefix) {
				continue
			}
			itemKey := strings.TrimPrefix(variable, prefix)
			itemKey = strings.TrimPrefix(itemKey, "_")
			itemKey = strings.Replace(itemKey, "_", keySeparator, -1)
			itemKey = strings.ToLower(itemKey)
			s.BindEnv(variable, itemKey)
		}
	}
}

func (s *Store) EnvVariable(opts ...EnvVariableOptions) *EnvVariableProvider {
	var environ = os.Environ()
	var provider = EnvVariableProvider{
		inMemory: InMemoryProvider{},
		bindings: make(map[string]string, len(environ)),
	}
	for _, opt := range opts {
		opt(&provider)
	}
	s.RegisterProvider("environ", provider.Items)
	return &provider
}

func (s *EnvVariableProvider) BindEnv(environmentVariable string, itemKey string) {
	s.inMemory.mut.Lock()
	defer s.inMemory.mut.Unlock()
	s.bindings[environmentVariable] = itemKey
}

func (s *EnvVariableProvider) Items() (ItemList, error) {
	environ := os.Environ()
	s.inMemory.mut.Lock()
	s.inMemory.items = make([]Item, 0, len(environ))
	s.inMemory.mut.Unlock()

	for _, env := range environ {
		splittedEnv := strings.SplitN(env, "=", 2)
		variable := splittedEnv[0]
		value := splittedEnv[1]
		key, has := s.bindings[variable]
		if has {
			s.inMemory.Add(Item{
				key:      key,
				value:    value,
				priority: s.priority,
			})
		}
	}

	return s.inMemory.Items()
}
