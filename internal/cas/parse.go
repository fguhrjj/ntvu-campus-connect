package cas

import (
	"fmt"
	"io"
	"strings"

	"golang.org/x/net/html"
)

type loginForm struct {
	Action, Execution, LT, EventID, CLLT, DLLT, Salt string
}

func parseLoginForm(r io.Reader) (loginForm, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return loginForm{}, err
	}
	formNode := findID(doc, "pwdFromId")
	if formNode == nil || formNode.Data != "form" {
		return loginForm{}, fmt.Errorf("missing pwdFromId form")
	}
	f := loginForm{Action: attr(formNode, "action")}
	fields := map[string]*string{
		"execution": &f.Execution,
		"lt":        &f.LT,
		"_eventId":  &f.EventID,
		"cllt":      &f.CLLT,
		"dllt":      &f.DLLT,
	}
	walk(formNode, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "input" {
			return
		}
		id, name, value := attr(n, "id"), attr(n, "name"), attr(n, "value")
		if target := fields[name]; target != nil {
			*target = value
		}
		if id == "pwdEncryptSalt" {
			f.Salt = strings.TrimSpace(value)
		}
	})
	if f.Action != "/authserver/login" || f.Execution == "" || f.EventID != "submit" || f.CLLT != "userNameLogin" || f.DLLT != "generalLogin" || len(f.Salt) != 16 {
		return loginForm{}, fmt.Errorf("pwdFromId fields do not match the expected login form")
	}
	if _, ok := fields["lt"]; !ok {
		return loginForm{}, fmt.Errorf("missing lt")
	}
	return f, nil
}

func parseElementText(r io.Reader, id string) (string, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return "", err
	}
	n := findID(doc, id)
	if n == nil {
		return "", fmt.Errorf("missing element %q", id)
	}
	return strings.TrimSpace(textContent(n)), nil
}

func parseErrorTip(r io.Reader) (string, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return "", err
	}
	form := findID(doc, "pwdFromId")
	if form != nil {
		if n := findID(form, "showErrorTip"); n != nil {
			if text := strings.TrimSpace(textContent(n)); text != "" {
				return text, nil
			}
		}
	}
	var found string
	walk(doc, func(n *html.Node) {
		if found == "" && n.Type == html.ElementNode && attr(n, "id") == "showErrorTip" {
			found = strings.TrimSpace(textContent(n))
		}
	})
	return found, nil
}

func findID(root *html.Node, id string) *html.Node {
	var found *html.Node
	walk(root, func(n *html.Node) {
		if found == nil && n.Type == html.ElementNode && attr(n, "id") == id {
			found = n
		}
	})
	return found
}

func walk(root *html.Node, visit func(*html.Node)) {
	visit(root)
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		walk(child, visit)
	}
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func textContent(n *html.Node) string {
	var b strings.Builder
	var collect func(*html.Node)
	collect = func(cur *html.Node) {
		if cur.Type == html.TextNode {
			b.WriteString(cur.Data)
		}
		for child := cur.FirstChild; child != nil; child = child.NextSibling {
			collect(child)
		}
	}
	collect(n)
	return b.String()
}
