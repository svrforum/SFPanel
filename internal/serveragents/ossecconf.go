package serveragents

import (
	"bytes"
	"cmp"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// OssecPlaceholder is the manager address the Wazuh agent package writes into
// ossec.conf, standing in until a real one is set.
const OssecPlaceholder = "MANAGER_IP"

// ossecDefaultGroup is the agent group enrollment asks for when none is given.
const ossecDefaultGroup = "default"

// OssecConf is what a Wazuh agent's ossec.conf says about its manager, read
// from the first <client> section directly inside a top-level <ossec_config>.
type OssecConf struct {
	Servers           []string `json:"servers"` // <client><server><address>, in order
	EnrollmentManager string   `json:"enrollment_manager"`
	Groups            string   `json:"groups"`
}

var (
	// ErrOssecUnreadable wraps the reason the file could not be read as XML.
	ErrOssecUnreadable = errors.New("ossec.conf is not readable XML")
	// ErrOssecNoClient means no top-level <ossec_config> holds a <client>.
	ErrOssecNoClient = errors.New("ossec.conf has no <client> section")
	// ErrOssecManyServers refuses to edit a <client> with more than one
	// <server>: failing over between managers is the operator's to set up.
	ErrOssecManyServers = errors.New("ossec.conf names several managers")
)

// ReadOssecConf reads the first <client> section of an ossec.conf. Servers
// holds one entry per <server>, empty for a <server> with no <address>.
// Values have the white space around them left out, and anything inside a
// comment is not read.
func ReadOssecConf(src []byte) (OssecConf, error) {
	client, err := ossecClient(src)
	if err != nil {
		return OssecConf{}, err
	}
	c := OssecConf{Servers: []string{}}
	for _, s := range client.childrenNamed("server") {
		c.Servers = append(c.Servers, s.childValue("address"))
	}
	if en := client.child("enrollment"); en != nil {
		c.EnrollmentManager = en.childValue("manager_address")
		c.Groups = en.childValue("groups")
	}
	return c, nil
}

// EditOssecConf points the first <client> section of an ossec.conf at
// manager: the <address> of its one <server>, and <manager_address> and
// <groups> (default when groups is empty) of its <enrollment>, each changed
// in place or added at the end of its parent. A section with no <server> or
// no <enrollment> gets one, enrollment enabled; an existing <enrollment> keeps
// its <enabled>. Every byte outside the values set and the elements added
// stays as it was, comments included. A section naming several managers is
// left to the operator: ErrOssecManyServers and nothing changed.
//
// When the file already says all this, changed is false and out is src.
func EditOssecConf(src []byte, manager, groups string) (out []byte, changed bool, err error) {
	client, err := ossecClient(src)
	if err != nil {
		return nil, false, err
	}
	servers := client.childrenNamed("server")
	if len(servers) > 1 {
		return nil, false, ErrOssecManyServers
	}
	manager = strings.TrimSpace(manager)
	groups = strings.TrimSpace(groups)
	if groups == "" {
		groups = ossecDefaultGroup
	}

	e := &xmlEditor{src: src, unit: ossecIndentUnit(src, client, servers)}
	var add []newElem
	if len(servers) == 1 {
		e.setLeaves(servers[0], newElem{name: "address", value: manager})
	} else {
		add = append(add, newElem{name: "server", children: []newElem{{name: "address", value: manager}}})
	}
	if en := client.child("enrollment"); en != nil {
		e.setLeaves(en, newElem{name: "manager_address", value: manager}, newElem{name: "groups", value: groups})
	} else {
		add = append(add, newElem{name: "enrollment", children: []newElem{
			{name: "enabled", value: "yes"},
			{name: "manager_address", value: manager},
			{name: "groups", value: groups},
		}})
	}
	e.appendChildren(client, add)

	out = e.apply()
	if bytes.Equal(out, src) {
		return src, false, nil
	}
	return out, true, nil
}

// ossecClient finds the first <client> directly inside a top-level
// <ossec_config>; a file may hold several <ossec_config> blocks.
func ossecClient(src []byte) (*xmlNode, error) {
	roots, err := parseXMLNodes(src)
	if err != nil {
		return nil, err
	}
	for _, r := range roots {
		if !r.is("ossec_config") {
			continue
		}
		if c := r.child("client"); c != nil {
			return c, nil
		}
	}
	return nil, ErrOssecNoClient
}

// ossecIndentUnit is one level of indentation in the file: how much further
// the <server> line is indented than the <client> line, or two spaces when
// that cannot be told.
func ossecIndentUnit(src []byte, client *xmlNode, servers []*xmlNode) string {
	if len(servers) == 1 {
		outer, ok1 := lineIndent(src, client.start)
		inner, ok2 := lineIndent(src, servers[0].start)
		if ok1 && ok2 && len(inner) > len(outer) && strings.HasPrefix(inner, outer) {
			return inner[len(outer):]
		}
	}
	return "  "
}

// xmlSpace is the white space XML allows between markup.
const xmlSpace = " \t\r\n"

// xmlNode is an element with the byte offsets of its tags, so it can be
// changed in place without encoding the file again.
type xmlNode struct {
	name       xml.Name
	start      int // '<' of the start tag
	openEnd    int // just past the start tag
	closeStart int // '<' of the end tag; equal to end for an empty-element tag <x/>
	end        int // just past the end tag
	children   []*xmlNode
	text       []xmlText // character data directly inside
	other      bool      // a comment, processing instruction or directive directly inside
}

type xmlText struct {
	start, end int
	data       string // decoded
}

func (n *xmlNode) is(local string) bool { return n.name.Space == "" && n.name.Local == local }

func (n *xmlNode) selfClosing() bool { return n.closeStart == n.end }

func (n *xmlNode) child(local string) *xmlNode {
	for _, c := range n.children {
		if c.is(local) {
			return c
		}
	}
	return nil
}

func (n *xmlNode) childrenNamed(local string) []*xmlNode {
	var out []*xmlNode
	for _, c := range n.children {
		if c.is(local) {
			out = append(out, c)
		}
	}
	return out
}

// value is the element's own text with the white space around it left out.
func (n *xmlNode) value() string {
	var b strings.Builder
	for _, t := range n.text {
		b.WriteString(t.data)
	}
	return strings.Trim(b.String(), xmlSpace)
}

func (n *xmlNode) childValue(local string) string {
	if c := n.child(local); c != nil {
		return c.value()
	}
	return ""
}

// parseXMLNodes reads src's elements, keeping where each tag sits. Several
// top-level elements are allowed. Anything the strict decoder refuses, an end
// tag that does not close the open element, and an element left open at the
// end are ErrOssecUnreadable.
func parseXMLNodes(src []byte) ([]*xmlNode, error) {
	d := xml.NewDecoder(bytes.NewReader(src))
	var roots, open []*xmlNode
	for {
		start := int(d.InputOffset())
		tok, err := d.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrOssecUnreadable, err)
		}
		end := int(d.InputOffset())
		var top *xmlNode
		if len(open) > 0 {
			top = open[len(open)-1]
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &xmlNode{name: t.Name, start: start, openEnd: end}
			if top == nil {
				roots = append(roots, n)
			} else {
				top.children = append(top.children, n)
			}
			open = append(open, n)
		case xml.EndElement:
			if top == nil || top.name != t.Name {
				line, _ := d.InputPos()
				return nil, fmt.Errorf("%w: line %d: unexpected </%s>", ErrOssecUnreadable, line, qualifiedName(t.Name))
			}
			top.closeStart, top.end = start, end
			open = open[:len(open)-1]
		case xml.CharData:
			if top != nil {
				top.text = append(top.text, xmlText{start: start, end: end, data: string(t)})
			}
		default:
			if top != nil {
				top.other = true
			}
		}
	}
	if len(open) > 0 {
		return nil, fmt.Errorf("%w: <%s> is never closed", ErrOssecUnreadable, qualifiedName(open[len(open)-1].name))
	}
	return roots, nil
}

func qualifiedName(n xml.Name) string {
	if n.Space == "" {
		return n.Local
	}
	return n.Space + ":" + n.Local
}

// lineIndent returns what stands between the start of at's line and at, and
// whether that is only spaces and tabs.
func lineIndent(src []byte, at int) (string, bool) {
	prefix := src[bytes.LastIndexByte(src[:at], '\n')+1 : at]
	if len(bytes.Trim(prefix, " \t")) != 0 {
		return "", false
	}
	return string(prefix), true
}

func escapeXML(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// newElem is an element to add: a leaf holding value, or a parent of
// children.
type newElem struct {
	name     string
	value    string
	children []newElem
}

// writeElems writes elems one per line at indent, their children one unit
// further in, each line ended by nl. With all three empty the elements are
// written on one line.
func writeElems(b *strings.Builder, elems []newElem, indent, unit, nl string) {
	for _, el := range elems {
		b.WriteString(indent + "<" + el.name + ">")
		if len(el.children) == 0 {
			b.WriteString(escapeXML(el.value))
		} else {
			b.WriteString(nl)
			writeElems(b, el.children, indent+unit, unit, nl)
			b.WriteString(indent)
		}
		b.WriteString("</" + el.name + ">" + nl)
	}
}

func inlineElems(elems []newElem) string {
	var b strings.Builder
	writeElems(&b, elems, "", "", "")
	return b.String()
}

type xmlEdit struct {
	start, end int
	text       string
}

// xmlEditor collects replacements of byte ranges of src and applies them at
// once, so every offset found while parsing stays valid until then.
type xmlEditor struct {
	src   []byte
	unit  string
	edits []xmlEdit
}

func (e *xmlEditor) replace(start, end int, text string) {
	e.edits = append(e.edits, xmlEdit{start: start, end: end, text: text})
}

// apply returns src with the edits made. Edits never overlap; two insertions
// at one offset keep the order they were made in.
func (e *xmlEditor) apply() []byte {
	slices.SortStableFunc(e.edits, func(a, b xmlEdit) int { return cmp.Compare(a.start, b.start) })
	var b bytes.Buffer
	at := 0
	for _, ed := range e.edits {
		b.Write(e.src[at:ed.start])
		b.WriteString(ed.text)
		at = ed.end
	}
	b.Write(e.src[at:])
	return b.Bytes()
}

// openTag is n's start tag written as one that is not empty: <x/> becomes <x>.
func (e *xmlEditor) openTag(n *xmlNode) string {
	tag := strings.TrimSuffix(string(e.src[n.start:n.openEnd]), "/>")
	return strings.TrimRight(tag, xmlSpace) + ">"
}

// setLeaves sets each leaf child of parent named in leaves, adding the ones it
// does not have.
func (e *xmlEditor) setLeaves(parent *xmlNode, leaves ...newElem) {
	var missing []newElem
	for _, l := range leaves {
		if c := parent.child(l.name); c != nil {
			e.setLeaf(c, l.value)
		} else {
			missing = append(missing, l)
		}
	}
	e.appendChildren(parent, missing)
}

// setLeaf makes n's text value. Only the text itself is replaced: the white
// space around it and any comment beside it stay. Text split around a comment
// is gathered into the first piece.
func (e *xmlEditor) setLeaf(n *xmlNode, value string) {
	if n.value() == value {
		return
	}
	text := escapeXML(value)
	if n.selfClosing() {
		e.replace(n.start, n.openEnd, e.openTag(n)+text+"</"+qualifiedName(n.name)+">")
		return
	}
	placed := false
	for _, t := range n.text {
		raw := e.src[t.start:t.end]
		lo := t.end - len(bytes.TrimLeft(raw, xmlSpace))
		hi := t.start + len(bytes.TrimRight(raw, xmlSpace))
		if lo >= hi {
			continue
		}
		if placed {
			e.replace(lo, hi, "")
			continue
		}
		e.replace(lo, hi, text)
		placed = true
	}
	if placed {
		return
	}
	if len(n.children) == 0 && !n.other {
		e.replace(n.openEnd, n.closeStart, text)
	} else {
		e.replace(n.openEnd, n.openEnd, text)
	}
}

// appendChildren adds elems at the end of parent. When parent's end tag has a
// line of its own they go on lines of their own above it, one indentation
// unit further in and with the file's line ends; otherwise they go on the end
// tag's line, right before it.
func (e *xmlEditor) appendChildren(parent *xmlNode, elems []newElem) {
	if len(elems) == 0 {
		return
	}
	if parent.selfClosing() {
		e.replace(parent.start, parent.openEnd, e.openTag(parent)+inlineElems(elems)+"</"+qualifiedName(parent.name)+">")
		return
	}
	indent, ownLine := lineIndent(e.src, parent.closeStart)
	if !ownLine {
		e.replace(parent.closeStart, parent.closeStart, inlineElems(elems))
		return
	}
	lineStart := parent.closeStart - len(indent)
	nl := "\n"
	if lineStart >= 2 && e.src[lineStart-2] == '\r' {
		nl = "\r\n"
	}
	var b strings.Builder
	writeElems(&b, elems, indent+e.unit, e.unit, nl)
	e.replace(lineStart, lineStart, b.String())
}
