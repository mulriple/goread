/*
 * Copyright (c) 2013 Matt Jibson <matt.jibson@gmail.com>
 *
 * Permission to use, copy, modify, and distribute this software for any
 * purpose with or without fee is hereby granted, provided that the above
 * copyright notice and this permission notice appear in all copies.
 *
 * THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
 * WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
 * MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
 * ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
 * WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
 * ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
 * OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
 */

package goapp

import (
	"appengine"
	"appengine/user"
	"bytes"
	"code.google.com/p/rsc/blog/atom"
	"encoding/xml"
	"fmt"
	mpg "github.com/mjibson/MiniProfiler/go/miniprofiler_gae"
	"github.com/mjibson/goon"
	"github.com/mjibson/rssgo"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"time"
)

func serveError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

type Includes struct {
	Angular      string
	BootstrapCss string
	BootstrapJs  string
	Jquery       string
	MiniProfiler template.HTML
	User         *User
}

var (
	Angular      string
	BootstrapCss string
	BootstrapJs  string
	Jquery       string
)

func init() {
	angular_ver := "1.0.5"
	bootstrap_ver := "2.3.1"
	jquery_ver := "1.9.1"

	if appengine.IsDevAppServer() {
		Angular = fmt.Sprintf("/static/js/angular-%v.js", angular_ver)
		BootstrapCss = fmt.Sprintf("/static/css/bootstrap-%v.css", bootstrap_ver)
		BootstrapJs = fmt.Sprintf("/static/js/bootstrap-%v.js", bootstrap_ver)
		Jquery = fmt.Sprintf("/static/js/jquery-%v.js", jquery_ver)
	} else {
		Angular = fmt.Sprintf("//ajax.googleapis.com/ajax/libs/angularjs/%v/angular.min.js", angular_ver)
		BootstrapCss = fmt.Sprintf("//netdna.bootstrapcdn.com/twitter-bootstrap/%v/css/bootstrap-combined.min.css", bootstrap_ver)
		BootstrapJs = fmt.Sprintf("//netdna.bootstrapcdn.com/twitter-bootstrap/%v/js/bootstrap.min.js", bootstrap_ver)
		Jquery = fmt.Sprintf("//ajax.googleapis.com/ajax/libs/jquery/%v/jquery.min.js", jquery_ver)
	}
}

func includes(c mpg.Context) *Includes {
	i := &Includes{
		Angular:      Angular,
		BootstrapCss: BootstrapCss,
		BootstrapJs:  BootstrapJs,
		Jquery:       Jquery,
		MiniProfiler: c.P.Includes(),
	}

	if u := user.Current(c); u != nil {
		gn := goon.FromContext(c)
		user := new(User)
		if e, err := gn.GetById(user, u.ID, 0, nil); err == nil && !e.NotFound {
			i.User = user
		}
	}

	return i
}

const atomDateFormat = "2006-01-02T15:04:05-07:00"

func ParseAtomDate(d atom.TimeStr) time.Time {
	t, err := time.Parse(atomDateFormat, string(d))
	if err != nil {
		return time.Time{}
	}
	return t
}

func ParseFeed(c appengine.Context, b []byte) (*Feed, []*Story) {
	f := Feed{}
	var s []*Story

	a := atom.Feed{}
	var atomerr, rsserr, rdferr error
	d := xml.NewDecoder(bytes.NewReader(b))
	d.CharsetReader = CharsetReader
	if atomerr = d.Decode(&a); atomerr == nil {
		f.Title = a.Title
		if a.Updated != "" {
			f.Updated = ParseAtomDate(a.Updated)
		}
		for _, l := range a.Link {
			if l.Rel != "self" {
				f.Link = l.Href
				break
			}
		}

		for _, i := range a.Entry {
			st := Story{
				Id:        i.ID,
				Title:     i.Title,
				Published: ParseAtomDate(i.Published),
				Updated:   ParseAtomDate(i.Updated),
			}
			if len(i.Link) > 0 {
				st.Link = i.Link[0].Href
			}
			if i.Author != nil {
				st.Author = i.Author.Name
			}
			if i.Content != nil {
				st.content, st.Summary = Sanitize(i.Content.Body)
			}
			st.Date = st.Updated.Unix()
			s = append(s, &st)
		}

		return parseFix(&f, s)
	}

	r := rssgo.Rss{}
	d = xml.NewDecoder(bytes.NewReader(b))
	d.CharsetReader = CharsetReader
	if rsserr = d.Decode(&r); rsserr == nil {
		f.Title = r.Title
		f.Link = r.Link
		if t, err := rssgo.ParseRssDate(r.LastBuildDate); err == nil {
			f.Updated = t
		} else {
			if t, err = rssgo.ParseRssDate(r.PubDate); err == nil {
				f.Updated = t
			}
		}

		for _, i := range r.Items {
			st := Story{
				Link:   i.Link,
				Author: i.Author,
			}
			if i.Title != "" {
				st.Title = i.Title
			} else if i.Description != "" {
				i.Title = i.Description
			} else {
				c.Errorf("rss feed requires title or description")
				return nil, nil
			}
			if i.Content != "" {
				st.content, st.Summary = Sanitize(i.Content)
			} else if i.Title != "" && i.Description != "" {
				st.content, st.Summary = Sanitize(i.Description)
			}
			if i.Guid != nil {
				st.Id = i.Guid.Guid
			} else {
				st.Id = i.Title
			}
			var t time.Time
			var err error
			if t, err = rssgo.ParseRssDate(i.PubDate); err == nil {
			} else if t, err = rssgo.ParseRssDate(i.Date); err == nil {
			} else if t, err = rssgo.ParseRssDate(i.Published); err == nil {
			} else {
				c.Errorf("could not parse date: %v, %v, %v", i.PubDate, i.Date, i.Published)
				t = time.Now()
			}
			st.Published = t
			st.Updated = t
			st.Date = t.Unix()

			s = append(s, &st)
		}

		return parseFix(&f, s)
	}

	rdf := RDF{}
	d = xml.NewDecoder(bytes.NewReader(b))
	d.CharsetReader = CharsetReader
	if rdferr = d.Decode(&rdf); rdferr == nil {
		if rdf.Channel != nil {
			f.Title = rdf.Channel.Title
			f.Link = rdf.Channel.Link
			if t, err := rssgo.ParseRssDate(rdf.Channel.Date); err == nil {
				f.Updated = t
			} else {
				c.Errorf("could not parse date: %v", rdf.Channel.Date)
			}
		}

		for _, i := range rdf.Item {
			st := Story{
				Id:     i.About,
				Title:  i.Title,
				Link:   i.Link,
				Author: i.Creator,
			}
			st.content, st.Summary = Sanitize(html.UnescapeString(i.Description))
			if i.About == "" && i.Link != "" {
				st.Id = i.Link
			} else if i.About == "" && i.Link == "" {
				c.Errorf("rdf error, no story id: %v", i)
				return nil, nil
			}
			if t, err := rssgo.ParseRssDate(i.Date); err == nil {
				st.Published = t
				st.Updated = t
				st.Date = t.Unix()
			} else {
				c.Errorf("could not parse date: %v", i.Date)
			}
			s = append(s, &st)
		}

		return parseFix(&f, s)
	}

	c.Errorf("atom parse error: %s", atomerr.Error())
	c.Errorf("xml parse error: %s", rsserr.Error())
	c.Errorf("rdf parse error: %s", rdferr.Error())
	return nil, nil
}

func parseFix(f *Feed, ss []*Story) (*Feed, []*Story) {
	for _, s := range ss {
		// if a story doesn't have a link, see if its id is a URL
		if s.Link == "" {
			if u, err := url.Parse(s.Id); err == nil {
				s.Link = u.String()
			}
		}
	}

	return f, ss
}
