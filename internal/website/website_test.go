package website

import (
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestNames(t *testing.T) {
	for name, want := range map[string][][]string{
		"DT GLOBAL ASIA PACIFIC PTY LTD":            {{"dt", "global"}},
		"LOCKHEED MARTIN AUSTRALIA PTY LTD":         {{"lockheed", "martin"}},
		"Jones Lang LaSalle (ACT) Pty Ltd":          {{"jones", "lang", "lasalle"}},
		"KONGSBERG DEFENCE & AEROSPACE AS":          {{"kongsberg", "defence", "and", "aerospace"}},
		"Data#3 Limited Formerly: Data #3 Business": {{"data", "3"}, {"data", "3", "business"}},
		"PARTECH PTY LIMITED T/A BYTES 'N COLOURS":  {{"partech"}, {"bytes", "n", "colours"}},
		"THE TRUSTEE FOR THE SMITH FAMILY TRUST":    {{"smith", "family"}},
		"MITSUBISHI HEAVY INDUSTRIES, LTD.":         {{"mitsubishi", "heavy", "industries"}},
		"SERENDIPITY WA PTY LTD":                    {{"serendipity"}},
	} {
		if got := Names(name); !reflect.DeepEqual(got, want) {
			t.Errorf("Names(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestMatch(t *testing.T) {
	for _, c := range []struct {
		name, text, want string
	}{
		{"DT GLOBAL ASIA PACIFIC PTY LTD", "Home - DT Global", "dt global"},
		{"DT GLOBAL ASIA PACIFIC PTY LTD", "dt-global", "dt global"},
		{"AUSTAL DEFENCE SHIPBUILDING AUSTRALIA PTY LTD", "Austal | Shipbuilding", "austal"},
		{"SPOTLESS FACILITY SERVICES PTY LTD", "Spotless - Facilities Management", "spotless"},
		{"TOLL REMOTE LOGISTICS PTY LTD", "Toll Group | Global Logistics", ""},
		{"SECURE JOURNEYS PTY LTD", "Secure Solutions Australia", ""},
		{"GLOBAL DATA SERVICES PTY LTD", "Global Data Corp", ""},
		{"Data#3 Limited", "Data#3 - Technology Solutions", "data 3"},
		{"LEIDOS AUSTRALIA PTY LIMITED", "Leidos Australia", "leidos"},
	} {
		if got := Match(Names(c.name), c.text); got != c.want {
			t.Errorf("Match(%q, %q) = %q, want %q", c.name, c.text, got, c.want)
		}
	}
}

func TestCandidates(t *testing.T) {
	got := Candidates(Supplier{Name: "AUSTAL DEFENCE SHIPBUILDING AUSTRALIA PTY LTD", Country: "AUSTRALIA"})
	want := []string{"austaldefenceshipbuilding.com.au", "austaldefenceshipbuilding.com", "austal-defence-shipbuilding.com.au",
		"austal-defence-shipbuilding.com", "austaldefence.com.au", "austaldefence.com", "austal.com.au", "austal.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Candidates = %v", got)
	}
}

func page(raw, body string) Page {
	u, _ := url.Parse(raw)
	return ParsePage(u, body)
}

func TestCheck(t *testing.T) {
	dt := Supplier{Name: "DT GLOBAL ASIA PACIFIC PTY LTD", ABN: "23006170869", Postcode: "3000", Country: "AUSTRALIA"}
	serendipity := Supplier{Name: "SERENDIPITY WA PTY LTD", ABN: "11111111111", Postcode: "6000", Country: "AUSTRALIA"}
	for _, c := range []struct {
		desc     string
		s        Supplier
		p        Page
		bar      Bar
		pass     bool
		evidence string
	}{
		{"ABN in footer", dt, page("https://dt-global.com/", `<title>Home</title><footer>ABN 23&nbsp;006 170 869</footer>`), Strict, true, "ABN on site"},
		{"site name", dt, page("https://dt-global.com/", `<meta property="og:site_name" content="DT Global"><p>Projects in Australia</p>`), Strict, true, `name in title: "dt global"`},
		{"other business", dt, page("https://example.com/", `<title>Acme</title>`), Plain, false, ""},
		{"one-word name needs postcode", serendipity, page("https://serendipity.com.au/", `<title>Serendipity Ice Cream</title><p>Sydney NSW 2000</p>`), Strict, false, ""},
		{"one-word name with postcode", serendipity, page("https://serendipity.com.au/", `<title>Serendipity</title><p>Perth WA 6000</p>`), Strict, true, `name in title: "serendipity"`},
		{"copyright line", dt, page("https://www.example.com.au/", `<title>Welcome</title><p>© 2026 DT Global. All rights reserved.</p>`), Strict, true, `name in copyright line: "dt global"`},
		{"script text ignored", dt, page("https://example.com/", `<title>Other</title><script>var s="ABN 23 006 170 869"</script>`), Plain, false, ""},
		{"first word only, guessed", Supplier{Name: "RIVERINA REDEVELOPMENT JOINT VENTURE PTY LTD", Postcode: "2650", Country: "AUSTRALIA"},
			page("https://www.riverina.com.au/", `<title>Riverina - Visit NSW</title>`), Strict, false, ""},
		{"first word only, searched", Supplier{Name: "AUSTAL DEFENCE SHIPBUILDING AUSTRALIA PTY LTD", Country: "AUSTRALIA"},
			page("https://www.austal.com/", `<title>Austal | Shipbuilding</title>`), Plain, true, `name in title: "austal"`},
		{"domain only, guessed", Supplier{Name: "CUSHMAN & WAKEFIELD PTY LTD", Country: "AUSTRALIA"},
			page("https://cushman-and-wakefield.com/", `<title>Welcome</title><p>Australia</p>`), Strict, false, ""},
		{"title is its own domain", Supplier{Name: "MINISTRY OF HEALTH", Postcode: "1590", Country: "AUSTRALIA"},
			page("https://ministryofhealth.com.au/", `<title>ministryofhealth.com.au</title><p>ministryofhealth.com.au</p>`), Strict, false, ""},
		{"joined leading part, searched", Supplier{Name: "MINTER ELLISON LAWYERS", Country: "AUSTRALIA"},
			page("https://www.minterellison.com/", `<title>Leading Australian law firm - MinterEllison</title>`), Plain, true, `name in title: "minter ellison"`},
		{"initials and domain, searched", Supplier{Name: "KELLOGG BROWN & ROOT PTY LTD", Country: "AUSTRALIA"},
			page("https://www.kbr.com/en-au/", `<title>KBR | Delivering Solutions</title>`), Plain, true, `name in title and domain: "kbr"`},
		{"first word and domain, searched", Supplier{Name: "Doma Barton Pty Ltd", Country: "AUSTRALIA"},
			page("https://domagroup.com.au/", `<title>DOMA Group</title>`), Plain, true, `name in title and domain: "doma"`},
		{"first word and domain, guessed", Supplier{Name: "Doma Barton Pty Ltd", Postcode: "2600", Country: "AUSTRALIA"},
			page("https://domagroup.com.au/", `<title>DOMA Group</title>`), Strict, false, ""},
		{"renamed business", Supplier{Name: "Duxton Water Ltd", Country: "AUSTRALIA"},
			page("https://www.rivco.com.au/", `<title>Rivco Australia</title><p>© Rivco Australia 2026</p>`), Plain, false, ""},
		{"placeholder page", dt, page("https://dt-global.com/", `<title>DT Global</title><h1>Launching Soon</h1>`), Plain, false, ""},
	} {
		r := Check(c.s, c.p, c.bar)
		if r.Passed != c.pass || r.Evidence != c.evidence {
			t.Errorf("%s: got %+v", c.desc, r)
		}
	}
}

func TestParse(t *testing.T) {
	for text, want := range map[string]string{
		"DT Global is a development firm.\nWEBSITE: https://dt-global.com/": "https://dt-global.com/",
		"**WEBSITE:** none":                   "",
		"WEBSITE: <www.ventia.com>":           "https://www.ventia.com",
		"Found it.\nWEBSITE: sitzler.com.au.": "https://sitzler.com.au",
	} {
		got, err := Parse(text)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", text, got, err, want)
		}
	}
	if _, err := Parse("I could not decide."); err == nil {
		t.Error("Parse without a WEBSITE line: want an error")
	}
}

func TestHome(t *testing.T) {
	if got := Home("https://www.boeing.com.au:443/?x=1#top"); got != "https://www.boeing.com.au/" {
		t.Errorf("Home = %q", got)
	}
	if got := Home("https://www.tbwa.com.au/page-not-found/"); got != "https://www.tbwa.com.au/" {
		t.Errorf("Home = %q", got)
	}
	if got := Home("https://besixwatpac.com//"); got != "https://besixwatpac.com/" {
		t.Errorf("Home = %q", got)
	}
}

func TestExcluded(t *testing.T) {
	if !Excluded("au.linkedin.com") || Excluded("dt-global.com") || !strings.HasPrefix(UserAgent, "spendingexplorer") {
		t.Error("Excluded")
	}
}
