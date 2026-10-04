package transpile

import "strings"

// validHTMLTags is the living-standard HTML element set xango recognizes
// for a lowercase (non-Component) tag. Deliberately excludes obsolete/
// deprecated elements (center, font, marquee, tt, ...) -- there's no
// good reason a new .xg file should start using one of those, and
// omitting them helps catch a typo'd real tag rather than silently
// accepting a fossil. isKnownTag below also accepts any name
// containing a hyphen unconditionally (a custom element, e.g.
// <my-widget> -- see the root README's "Components" section), so this
// list only needs to cover standard elements.
var validHTMLTags = map[string]bool{
	"a": true, "abbr": true, "address": true, "area": true, "article": true,
	"aside": true, "audio": true, "b": true, "base": true, "bdi": true,
	"bdo": true, "blockquote": true, "body": true, "br": true, "button": true,
	"canvas": true, "caption": true, "cite": true, "code": true, "col": true,
	"colgroup": true, "data": true, "datalist": true, "dd": true, "del": true,
	"details": true, "dfn": true, "dialog": true, "div": true, "dl": true,
	"dt": true, "em": true, "embed": true, "fieldset": true, "figcaption": true,
	"figure": true, "footer": true, "form": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "h5": true, "h6": true, "head": true, "header": true,
	"hgroup": true, "hr": true, "html": true, "i": true, "iframe": true,
	"img": true, "input": true, "ins": true, "kbd": true, "label": true,
	"legend": true, "li": true, "link": true, "main": true, "map": true,
	"mark": true, "menu": true, "meta": true, "meter": true, "nav": true,
	"noscript": true, "object": true, "ol": true, "optgroup": true,
	"option": true, "output": true, "p": true, "param": true, "picture": true,
	"pre": true, "progress": true, "q": true, "rp": true, "rt": true,
	"ruby": true, "s": true, "samp": true, "script": true, "search": true,
	"section": true, "select": true, "slot": true, "small": true,
	"source": true, "span": true, "strong": true, "style": true, "sub": true,
	"summary": true, "sup": true, "svg": true, "table": true, "tbody": true,
	"td": true, "template": true, "textarea": true, "tfoot": true, "th": true,
	"thead": true, "time": true, "title": true, "tr": true, "track": true,
	"u": true, "ul": true, "var": true, "video": true, "wbr": true,
}

// validHTMLAttrs is the global-attribute set plus commonly-used
// element-specific attributes xango recognizes on a lowercase tag.
// data-* and aria-* are accepted unconditionally by isKnownAttr below
// (both are explicitly open-ended extension points per the HTML/ARIA
// specs, not a fixed list), so this only needs to cover everything
// else. Deliberately includes a handful of deprecated-but-still
// browser-accepted presentational attributes (align, bgcolor, border,
// ...) -- rejecting a real attribute someone pasted from existing
// markup would be a worse false positive than tolerating a fossil.
var validHTMLAttrs = map[string]bool{
	// Global attributes.
	"accesskey": true, "autocapitalize": true, "autofocus": true,
	"class": true, "contenteditable": true, "dir": true, "draggable": true,
	"enterkeyhint": true, "exportparts": true, "hidden": true, "id": true,
	"inert": true, "inputmode": true, "is": true, "itemid": true,
	"itemprop": true, "itemref": true, "itemscope": true, "itemtype": true,
	"lang": true, "nonce": true, "part": true, "popover": true,
	"popovertarget": true, "popovertargetaction": true, "role": true,
	"slot": true, "spellcheck": true, "style": true, "tabindex": true,
	"title": true, "translate": true,

	// Common element-specific attributes.
	"href": true, "target": true, "download": true, "rel": true,
	"hreflang": true, "type": true, "ping": true, "referrerpolicy": true,
	"src": true, "srcset": true, "sizes": true, "alt": true, "width": true,
	"height": true, "loading": true, "decoding": true, "usemap": true,
	"ismap": true, "crossorigin": true, "media": true, "disabled": true,
	"form": true, "formaction": true, "formenctype": true, "formmethod": true,
	"formnovalidate": true, "formtarget": true, "name": true, "value": true,
	"checked": true, "selected": true, "required": true, "readonly": true, "placeholder": true,
	"autocomplete": true, "maxlength": true, "minlength": true, "min": true,
	"max": true, "step": true, "pattern": true, "multiple": true, "size": true,
	"accept": true, "capture": true, "list": true, "cols": true, "rows": true,
	"wrap": true, "action": true, "method": true, "enctype": true,
	"novalidate": true, "for": true, "colspan": true, "rowspan": true,
	"scope": true, "headers": true, "cite": true, "datetime": true,
	"open": true, "reversed": true, "start": true, "high": true, "low": true,
	"optimum": true, "kind": true, "srclang": true, "default": true,
	"controls": true, "autoplay": true, "loop": true, "muted": true,
	"poster": true, "preload": true, "playsinline": true, "allow": true,
	"allowfullscreen": true, "sandbox": true, "srcdoc": true,
	"integrity": true, "defer": true, "async": true, "nomodule": true,
	"charset": true, "content": true, "http-equiv": true, "property": true,
	"scoped": true, "shape": true, "coords": true, "span": true,
	"dropzone": true,

	// Deprecated but still browser-accepted presentational attributes.
	"align": true, "valign": true, "bgcolor": true, "background": true,
	"border": true, "cellpadding": true, "cellspacing": true,
}

// isKnownTag reports whether name is a real HTML element xango will
// render as-is, or a custom element (any name containing a hyphen is
// always accepted, per the Custom Elements spec and this repo's own
// <my-widget> convention).
func isKnownTag(name string) bool {
	if strings.ContainsRune(name, '-') {
		return true
	}
	return validHTMLTags[name]
}

// isKnownAttr reports whether name is a real HTML attribute xango will
// set as-is, or a data-*/aria-* attribute (both open-ended extension
// points, not a fixed list, per the HTML and ARIA specs).
func isKnownAttr(name string) bool {
	if strings.HasPrefix(name, "data-") || strings.HasPrefix(name, "aria-") {
		return true
	}
	return validHTMLAttrs[name]
}
