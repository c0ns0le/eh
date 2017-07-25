package secrets

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"

	"github.com/hashicorp/hcl/hcl/ast"
	"github.com/hashicorp/hcl/hcl/token"
	"github.com/pkg/errors"
)

type operation int

const (
	opEncrypt operation = 1
	opDecrypt operation = 2
)

func processNode(name string, node ast.Node, op operation, key *EncryptionKey, protect map[string]bool) error {
	switch t := node.(type) {
	case *ast.File:
		if err := processNode(name, t.Node, op, key, protect); err != nil {
			return errors.Wrapf(err, "failed processNode %q", name)
		}
	case *ast.ListType:
		for _, node := range t.List {
			if err := processNode(name, node, op, key, protect); err != nil {
				return errors.Wrapf(err, "failed to processNode %q", name)
			}
		}
	case *ast.ObjectType:
		if err := processList(t.List, op, key, protect); err != nil {
			return errors.Wrapf(err, "failed to processList %q", name)
		}

	case *ast.ObjectList:
		if err := processList(t, op, key, protect); err != nil {
			return errors.Wrapf(err, "failed to processList %q", name)
		}
	case *ast.ObjectItem:
		if err := processItem(t, op, key, protect); err != nil {
			return errors.Wrapf(err, "failed to processItem %q", name)
		}
	case *ast.LiteralType:
		if t.Token.Type == token.STRING && protect[name] {
			value, err := strconv.Unquote(t.Token.Text)
			if err != nil {
				return errors.Wrapf(err, "failed to Unquote %q", name)
			}

			switch op {
			case opEncrypt:
				ciphertext, err := key.Encrypt([]byte(value))
				if err != nil {
					return errors.Wrapf(err, "failed to Encrypt %q", name)
				}

				encoded := base64.RawURLEncoding.EncodeToString(ciphertext)
				t.Token.Text = strconv.Quote(encoded)
			case opDecrypt:
				decoded, err := base64.RawURLEncoding.DecodeString(value)
				if err != nil {
					return errors.Wrapf(err, "failed to decode base64 value %q", value)
				}

				plaintext, err := key.Decrypt(decoded)
				if err != nil {
					return errors.Wrapf(err, "failed to decrypt value %q", value)
				}

				t.Token.Text = strconv.Quote(string(plaintext))
			default:
				return fmt.Errorf("failed because of unknown operation %d", op)
			}
		}
	default:
		return fmt.Errorf("failed because of unknown node type %v", reflect.TypeOf(t))
	}

	return nil
}

func processList(list *ast.ObjectList, op operation, key *EncryptionKey, protect map[string]bool) error {
	for _, item := range list.Items {
		if err := processItem(item, op, key, protect); err != nil {
			return errors.Wrap(err, "failed to processItem")
		}
	}

	return nil
}

func processItem(item *ast.ObjectItem, op operation, key *EncryptionKey, protect map[string]bool) error {
	name := item.Keys[0].Token.Text
	if err := processNode(name, item.Val, op, key, protect); err != nil {
		return errors.Wrapf(err, "failed to processNode %q", name)
	}
	return nil
}

func addEncryptionKey(node ast.Node, key *EncryptionKey) error {
	keyEntry, err := getHeaderValue(node, "key")
	if err != nil {
		return errors.Wrap(err, "failed to getHeaderValue for 'key'")
	}

	marshaledKey, err := json.Marshal(key)
	if err != nil {
		return errors.Wrap(err, "failed to Marshal key")
	}

	encodedKey := base64.RawURLEncoding.EncodeToString(marshaledKey)
	keyEntry.Token.Text = strconv.Quote(encodedKey)

	encryptedEntry, err := getHeaderValue(node, "encrypted")
	if err != nil {
		return errors.Wrap(err, "failed to getHeaderValue for 'encrypted'")
	}

	encryptedEntry.Token.Text = "true"

	return nil
}

func removeEncryptionKey(node ast.Node) error {
	keyEntry, err := getHeaderValue(node, "key")
	if err != nil {
		return errors.Wrap(err, "failed to getHeaderValue for 'key'")
	}

	keyEntry.Token.Text = strconv.Quote("")

	encryptedEntry, err := getHeaderValue(node, "encrypted")
	if err != nil {
		return errors.Wrap(err, "failed to getHeaderValue for 'encrypted'")
	}

	encryptedEntry.Token.Text = "false"

	return nil
}

func getHeaderValue(node ast.Node, name string) (*ast.LiteralType, error) {
	var list *ast.ObjectList

	file, ok := node.(*ast.File)
	if ok {
		list, ok = file.Node.(*ast.ObjectList)
	} else {
		list, ok = node.(*ast.ObjectList)
	}

	if !ok {
		return nil, errors.New("failed, unexpected .hcl format")
	}

	ehcl := list.Filter("ehcl")
	if len(ehcl.Items) != 1 {
		return nil, errors.New("failed, must have 'ehcl' element")
	}

	obj, ok := ehcl.Items[0].Val.(*ast.ObjectType)
	if !ok {
		return nil, errors.New("failed, invalid 'ehcl' element")
	}

	keyEntry := obj.List.Filter(name)
	if len(keyEntry.Items) == 0 {
		return nil, fmt.Errorf("failed, no %q element found in 'ehcl'", name)
	}

	if len(keyEntry.Items) > 1 {
		return nil, fmt.Errorf("failed, multiple %q elements found in 'ehcl'", name)
	}

	val, ok := keyEntry.Items[0].Val.(*ast.LiteralType)
	if !ok {
		return nil, fmt.Errorf("failed, invalid %q element in 'ehcl'", name)
	}

	return val, nil
}