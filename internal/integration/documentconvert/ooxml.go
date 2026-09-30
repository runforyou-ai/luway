//go:build server

package documentconvert

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"io"
	"io/fs"
	"path"
	"strings"
)

// relationshipNamespace 是 OOXML 关系引用属性的命名空间，其属性以 r: 前缀区分同名属性。
const relationshipNamespace = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"

// xmlNode 表示 OOXML 部件中的元素，名称与属性取本地名，Text 为元素内的直接文本。
type xmlNode struct {
	Name     string
	Attrs    map[string]string
	Children []*xmlNode
	Text     string
}

// child 返回首个指定名称的子元素，不存在时返回 nil。
func (n *xmlNode) child(name string) *xmlNode {
	if n == nil {
		return nil
	}
	for _, child := range n.Children {
		if child.Name == name {
			return child
		}
	}
	return nil
}

// elements 返回子元素列表，元素不存在时返回空列表。
func (n *xmlNode) elements() []*xmlNode {
	if n == nil {
		return nil
	}
	return n.Children
}

// attr 返回属性值，元素不存在时返回空字符串。
func (n *xmlNode) attr(name string) string {
	if n == nil {
		return ""
	}
	return n.Attrs[name]
}

// relationship 表示部件关系的类型、目标地址和目标模式，类型取类型地址的最后一段。
type relationship struct {
	Type     string
	Target   string
	External bool
}

// readPart 读取压缩包中的 XML 部件并解析为元素树，部件缺失时返回 nil。
func readPart(archive *zip.Reader, name string) (*xmlNode, error) {
	file, err := archive.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoder := xml.NewDecoder(file)
	root := &xmlNode{}
	stack := []*xmlNode{root}
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return root, nil
		}
		if err != nil {
			return nil, err
		}
		switch token := token.(type) {
		case xml.StartElement:
			node := &xmlNode{Name: token.Name.Local, Attrs: make(map[string]string, len(token.Attr))}
			for _, attr := range token.Attr {
				key := attr.Name.Local
				if attr.Name.Space == relationshipNamespace {
					key = "r:" + key
				}
				node.Attrs[key] = attr.Value
			}
			parent := stack[len(stack)-1]
			parent.Children = append(parent.Children, node)
			stack = append(stack, node)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			stack[len(stack)-1].Text += string(token)
		}
	}
}

// readRelationships 读取部件的关系表，内部目标解析为压缩包内的绝对路径。
func readRelationships(archive *zip.Reader, part string) (map[string]relationship, error) {
	directory, name := path.Split(part)
	root, err := readPart(archive, directory+"_rels/"+name+".rels")
	if err != nil {
		return nil, err
	}
	relationships := map[string]relationship{}
	for _, item := range root.child("Relationships").elements() {
		target, external := item.attr("Target"), item.attr("TargetMode") == "External"
		// 内部目标按部件所在目录解析，以斜杠开头时从压缩包根目录解析。
		if !external {
			if strings.HasPrefix(target, "/") {
				target = strings.TrimPrefix(target, "/")
			} else {
				target = path.Join(directory, target)
			}
		}
		relationships[item.attr("Id")] = relationship{Type: path.Base(item.attr("Type")), Target: target, External: external}
	}
	return relationships, nil
}
