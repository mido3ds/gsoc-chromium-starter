package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"strings"
	"time"

	"github.com/mafredri/cdp"
	"github.com/mafredri/cdp/devtool"
	"github.com/mafredri/cdp/protocol/dom"
	"github.com/mafredri/cdp/protocol/page"
	"github.com/mafredri/cdp/rpcc"
	"golang.org/x/net/html"
)

func main() {
	cnumber := flag.Int("cnumber", 10, "num of commits to load")
	repurl := flag.String("repurl", "https://chromium.googlesource.com/chromiumos/platform/tast-tests/", "repo url")
	branch := flag.String("branch", "main", "branch name")
	timeout := flag.Int("timeout", 5, "timeout in seconds")
	cmtsPath := flag.String("cmtspath", "", "path to commit files directory")
	flag.Parse()

	if *timeout <= 0 {
		log.Fatal("invalid timeout parameter")
	}
	if *branch == "" {
		log.Fatal("empty branch is invalid")
	}
	if *repurl == "" {
		log.Fatal("empty url is invalid")
	}
	if *cnumber <= 0 {
		log.Fatal("invalid cnumber")
	}

	err := run(time.Duration(*timeout)*time.Second, *cmtsPath, *repurl, *branch, *cnumber)
	if err != nil {
		log.Fatal(err)
	}
}

func run(timeout time.Duration, cmtsPath, repurl, branch string, cnumber int) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	devt := devtool.New("http://127.0.0.1:9222")
	pt, err := devt.Get(ctx, devtool.Page)
	if err != nil {
		pt, err = devt.Create(ctx)
		if err != nil {
			return err
		}
	}

	conn, err := rpcc.DialContext(ctx, pt.WebSocketDebuggerURL)
	if err != nil {
		return err
	}
	defer conn.Close()

	c := cdp.NewClient(conn)

	domContent, err := c.Page.DOMContentEventFired(ctx)
	if err != nil {
		return err
	}
	defer domContent.Close()

	if err = c.Page.Enable(ctx); err != nil {
		return err
	}

	m, err := fetchLink(c, ctx, domContent, repurl)
	if err != nil {
		return err
	}

	link, err := getMainLink(m, branch)
	if err != nil {
		return err
	}

	for i := 0; i < cnumber; i++ {
		// fetch commit page
		p, err := fetchLink(c, ctx, domContent, link)
		if err != nil {
			return err
		}

		// get commit
		cmt, err := getCurrentCommit(p)
		if err != nil {
			return err
		}

		// get next link
		link, err = getParentCommitLink(p, repurl)
		if err != nil {
			return err
		}

		// get commit message
		msg, err := getCommitMessage(p)
		if err != nil {
			return err
		}

		// write commit message
		err = ioutil.WriteFile(cmtsPath+cmt+".commit", []byte(msg), 0644)
		if err != nil {
			return err
		}
	}

	return nil
}

func fetchLink(c *cdp.Client, ctx context.Context, domContent page.DOMContentEventFiredClient, url string) (string, error) {
	navArgs := page.NewNavigateArgs(url)
	_, err := c.Page.Navigate(ctx, navArgs)
	if err != nil {
		return "", err
	}

	if _, err = domContent.Recv(); err != nil {
		return "", err
	}

	doc, err := c.DOM.GetDocument(ctx, nil)
	if err != nil {
		return "", err
	}

	result, err := c.DOM.GetOuterHTML(ctx, &dom.GetOuterHTMLArgs{
		NodeID: &doc.Root.NodeID,
	})
	if err != nil {
		return "", err
	}
	return result.OuterHTML, nil
}

func getMainLink(r, branch string) (string, error) {
	doc, err := html.Parse(strings.NewReader(r))
	if err != nil {
		return "", err
	}
	var f func(*html.Node) (string, error)
	f = func(n *html.Node) (string, error) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, atr := range n.Attr {
				if atr.Key == "href" && strings.Contains(atr.Val, "/"+branch) {
					return atr.Val, nil
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			l, err := f(c)
			if err == nil {
				return l, nil
			}
		}
		return "", fmt.Errorf("can't find link!")
	}
	s, err := f(doc)
	if err != nil {
		return "", err
	}
	return "https://chromium.googlesource.com" + s, nil
}

func getCurrentCommit(r string) (string, error) {
	doc, err := html.Parse(strings.NewReader(r))
	if err != nil {
		return "", err
	}
	var f func(*html.Node) (string, error)
	f = func(n *html.Node) (string, error) {
		if n.Type == html.TextNode {
			if n.Data == "commit" {
				return n.Parent.NextSibling.FirstChild.Data, nil
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			l, err := f(c)
			if err == nil {
				return l, nil
			}
		}
		return "", fmt.Errorf("can't find commit!")
	}
	s, err := f(doc)
	if err != nil {
		return "", err
	}
	return s, nil
}

func getCommitMessage(r string) (string, error) {
	doc, err := html.Parse(strings.NewReader(r))
	if err != nil {
		return "", err
	}
	var f2 func(*html.Node) (string, error)
	f2 = func(n *html.Node) (string, error) {
		if n.Type == html.TextNode {
			return n.Data, nil
		}
		total := ""
		m := 0
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			l, err := f2(c)
			if err == nil {
				total += l
				m++
			}
		}
		if m == 0 {
			return "", fmt.Errorf("can't find text!")
		}
		return total, nil
	}
	var f func(*html.Node) (string, error)
	f = func(n *html.Node) (string, error) {
		if n.Type == html.ElementNode && n.Data == "pre" {
			return f2(n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			l, err := f(c)
			if err == nil {
				return l, nil
			}
		}
		return "", fmt.Errorf("can't find commit!")
	}
	s, err := f(doc)
	if err != nil {
		return "", err
	}
	return s, nil
}

func getParentCommitLink(r, repurl string) (string, error) {
	doc, err := html.Parse(strings.NewReader(r))
	if err != nil {
		return "", err
	}
	var f func(*html.Node) (string, error)
	f = func(n *html.Node) (string, error) {
		if n.Type == html.TextNode {
			if n.Data == "parent" {
				return n.Parent.NextSibling.FirstChild.FirstChild.Data, nil
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			l, err := f(c)
			if err == nil {
				return l, nil
			}
		}
		return "", fmt.Errorf("can't find commit!")
	}
	s, err := f(doc)
	if err != nil {
		return "", err
	}
	return repurl + "/+/" + s, nil
}
