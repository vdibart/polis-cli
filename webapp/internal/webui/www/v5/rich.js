// The sheet's rich text: a post's body edited as formatted text, written back
// as markdown. The editor is Milkdown kit (the vendored, pinned module in
// /vendor/, loaded only when the sheet first needs it); this file is the part
// that is ours: what the editor is given, how it writes markdown back, and the
// commands behind the sheet's formatting buttons.
//
// ⭐ THE SEAMS STAY OUTSIDE THE EDITOR. It sees the body only: no frontmatter
// (the server makes that again on republishing, and signs), and not the blank
// lines before the body or the whitespace after it, which go back exactly as
// they came.
//
// ⭐ AN UNTOUCHED BODY IS NEVER RE-SERIALISED. markdown() answers the very text
// the editor was given until the document differs from the one it loaded
// (undoing back to it counts as untouched), so a post opened and left alone
// keeps its bytes, and its signature. Only an edited body is written out by
// the serializer, configured below so an edit changes only its own region.
(function (root) {
  'use strict';

  // seams splits text into what the editor is given (core) and what stays
  // outside it: the blank lines before (lead) and the whitespace after (tail).
  function seams(text) {
    text = String(text == null ? '' : text);
    var lead = (text.match(/^(?:[ \t]*\n)*/) || [''])[0];
    var rest = text.slice(lead.length);
    var tail = (rest.match(/\s*$/) || [''])[0];
    return { lead: lead, core: rest.slice(0, rest.length - tail.length), tail: tail };
  }

  // serializerOptions are remark-stringify's choices taken from the post
  // itself: its own bullet and its own thematic break, so an edit does not
  // rewrite every list and rule in the post. With none to copy, a bullet is
  // "-" (the commonest on polis sites) and a rule "***" (a body that begins
  // with "---" reads as frontmatter to anything that strips it).
  function serializerOptions(body) {
    var bullet = (String(body).match(/^ {0,3}([-*+]) (?!\[[ xX]\] )\S/m) || [])[1] || '-';
    var rule = (String(body).match(/^ {0,3}([-*_])(?: *\1){2,} *$/m) || [])[1] || '*';
    return { bullet: bullet, bulletOther: bullet === '-' ? '*' : '-', rule: rule };
  }

  // bareLink is the serializer's link handler: a link whose text is its own
  // address goes back as the bare address (GFM's autolink, which sites render
  // as a link), not remark's "<https://…>"; any other link as remark writes it.
  function bareLink(fallback) {
    return function (node, parent, state, info) {
      var c = node.children || [];
      var only = c.length === 1 && c[0].type === 'text' ? c[0].value : null;
      if (!node.title && only && /^(https?:\/\/|mailto:)/i.test(node.url) &&
        (only === node.url || 'mailto:' + only === node.url) && !/[\s<>]/.test(only)) return only;
      return fallback(node, parent, state, info);
    };
  }

  // noLeadingDashRule keeps a body from beginning with a "---" rule, which
  // frontmatter strippers would take for the start of frontmatter.
  function noLeadingDashRule(md) {
    return md.replace(/^ {0,3}-(?: *-){2,} *(?=\n|$)/, '***');
  }

  // withoutEmptyParagraphs is doc without the empty paragraphs a writer
  // leaves by pressing return twice, which the serializer would write as
  // "<br />" lines: a copy, so the document and its undo history are untouched.
  function withoutEmptyParagraphs(node) {
    if (node.isTextblock || node.isLeaf) return node;
    var kept = [];
    node.forEach(function (c) {
      if (!(c.type.name === 'paragraph' && c.content.size === 0)) kept.push(withoutEmptyParagraphs(c));
    });
    if (!kept.length) return node;
    return node.copy(node.content.constructor.fromArray(kept));
  }

  // The commands the sheet's buttons name (data-fmt), in rich text.
  var COMMANDS = ['body', 'heading', 'bold', 'italic', 'code', 'link', 'quote', 'list', 'numbers', 'divider', 'undo', 'redo'];

  // mount puts an editor in el holding text (markdown), and answers its
  // handle. kit is the vendored module; opts.onUpdate(handle) runs on every
  // change of the document or the selection, and opts.askLink(current) answers
  // the address for a link ('' removes it, null leaves it).
  async function mount(kit, el, text, opts) {
    opts = opts || {};
    var core = kit.core, cm = kit.commonmark, utils = kit.utils;
    var s = seams(text);
    var handle = null, initial = null;
    var watch = utils.$prose(function () {
      return new kit.state.Plugin({
        view: function () {
          return {
            update: function (view, prev) {
              if (handle && opts.onUpdate && (!view.state.doc.eq(prev.doc) || !view.state.selection.eq(prev.selection))) opts.onUpdate(handle);
            },
          };
        },
      });
    });
    var editor = await core.Editor.make()
      .config(function (ctx) {
        ctx.set(core.rootCtx, el);
        ctx.set(core.defaultValueCtx, s.core);
        ctx.update(core.remarkStringifyOptionsCtx, function (o) {
          var out = Object.assign({}, o, serializerOptions(s.core));
          out.handlers = Object.assign({}, o.handlers, { link: bareLink(kit.defaultHandlers.link) });
          return out;
        });
      })
      .use(cm.commonmark).use(kit.gfm.gfm).use(kit.history.history).use(kit.clipboard.clipboard).use(watch)
      .create();

    function view() { return editor.action(function (ctx) { return ctx.get(core.editorViewCtx); }); }
    initial = view().state.doc;
    function call(cmd, payload) { return editor.action(utils.callCommand(cmd.key, payload)); }
    function changed() { return !view().state.doc.eq(initial); }

    // What the selection is in, for the buttons' pressed state.
    function active() {
      var st = view().state, sel = st.selection, $f = sel.$from, a = {};
      ['strong', 'emphasis', 'inlineCode', 'link'].forEach(function (m) {
        var t = st.schema.marks[m];
        a[m] = !!t && (sel.empty ? !!t.isInSet(st.storedMarks || $f.marks()) : st.doc.rangeHasMark(sel.from, sel.to, t));
      });
      var out = { bold: a.strong, italic: a.emphasis, code: a.inlineCode, link: a.link,
        heading: $f.parent.type.name === 'heading', body: $f.parent.type.name === 'paragraph',
        quote: false, list: false, numbers: false };
      for (var d = $f.depth; d > 0; d--) {
        var n = $f.node(d).type.name;
        if (n === 'blockquote') out.quote = true;
        if (n === 'bullet_list' && !out.numbers) out.list = true;
        if (n === 'ordered_list' && !out.list) out.numbers = true;
      }
      return out;
    }

    // list makes the selection a list of the type named, or, when it is
    // one already, ordinary paragraphs again; a list of the other kind is
    // switched in place.
    function list(name) {
      var v = view(), st = v.state, $f = st.selection.$from, type = st.schema.nodes[name];
      for (var d = $f.depth; d > 0; d--) {
        var n = $f.node(d);
        if (n.type.name === 'bullet_list' || n.type.name === 'ordered_list') {
          if (n.type === type) return kit.schemaList.liftListItem(st.schema.nodes.list_item)(st, v.dispatch);
          v.dispatch(st.tr.setNodeMarkup($f.before(d), type, n.type.name === 'ordered_list' ? {} : null));
          return true;
        }
      }
      return call(name === 'bullet_list' ? cm.wrapInBulletListCommand : cm.wrapInOrderedListCommand);
    }

    function link() {
      var v = view(), st = v.state, sel = st.selection, mark = st.schema.marks.link;
      var current = '';
      st.doc.nodesBetween(sel.from, Math.max(sel.to, sel.from + 1), function (n) {
        var m = mark.isInSet(n.marks);
        if (m && !current) current = m.attrs.href;
      });
      var href = opts.askLink ? opts.askLink(current) : null;
      if (href === null || href === undefined) return false;
      href = String(href).trim();
      var tr = st.tr;
      if (sel.empty) {
        if (!href) return false;
        tr = tr.insert(sel.from, st.schema.text(href, [mark.create({ href: href })]));
      } else {
        tr = tr.removeMark(sel.from, sel.to, mark);
        if (href) tr = tr.addMark(sel.from, sel.to, mark.create({ href: href }));
      }
      v.dispatch(tr);
      return true;
    }

    function firstBlock() {
      var $f = view().state.selection.$from;
      return $f.depth >= 1 && $f.index(0) === 0;
    }

    // run applies one of COMMANDS, and answers whether it did anything.
    function run(name) {
      var r = false;
      switch (name) {
        case 'bold': r = call(cm.toggleStrongCommand); break;
        case 'italic': r = call(cm.toggleEmphasisCommand); break;
        case 'code': r = call(cm.toggleInlineCodeCommand); break;
        // A post's title is its first line, and a "# " heading there is the
        // title as the server reads it; further down, a heading is "## ".
        case 'heading': r = active().heading ? call(cm.turnIntoTextCommand) : call(cm.wrapInHeadingCommand, firstBlock() ? 1 : 2); break;
        case 'body': r = call(cm.turnIntoTextCommand); break;
        case 'quote': {
          if (active().quote) { var v = view(); r = kit.commands.lift(v.state, v.dispatch); } else r = call(cm.wrapInBlockquoteCommand);
          break;
        }
        case 'list': r = list('bullet_list'); break;
        case 'numbers': r = list('ordered_list'); break;
        case 'divider': r = call(cm.insertHrCommand); break;
        case 'undo': r = call(kit.history.undoCommand); break;
        case 'redo': r = call(kit.history.redoCommand); break;
        case 'link': r = link(); break;
      }
      view().focus();
      return !!r;
    }

    handle = {
      // markdown is the body as it stands: the text given, byte for byte,
      // while untouched; otherwise the serializer's, inside the same seams.
      markdown: function () {
        if (!changed()) return text;
        var serialize = editor.action(function (ctx) { return ctx.get(core.serializerCtx); });
        var md = noLeadingDashRule(serialize(withoutEmptyParagraphs(view().state.doc)).replace(/\n+$/, ''));
        return md ? s.lead + md + s.tail : '';
      },
      changed: changed,
      active: active,
      run: run,
      focus: function () { view().focus(); },
      destroy: function () { return editor.destroy(); },
      _view: view, // the ProseMirror view, for the tests
    };
    return handle;
  }

  var rich = { seams: seams, serializerOptions: serializerOptions, bareLink: bareLink, noLeadingDashRule: noLeadingDashRule, COMMANDS: COMMANDS, mount: mount };
  root.PolisV5Rich = rich;
  if (typeof module !== 'undefined' && module.exports) module.exports = rich;
})(typeof window !== 'undefined' ? window : globalThis);
