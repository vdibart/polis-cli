// Milkdown WYSIWYG Editor Bridge
// ES module that wraps Milkdown with a global MilkdownBridge API for multi-instance editing.

import { Editor, rootCtx, defaultValueCtx, editorViewCtx } from '@milkdown/kit/core';
import { commonmark } from '@milkdown/kit/preset/commonmark';
import { getMarkdown, replaceAll } from '@milkdown/kit/utils';
import { listener, listenerCtx } from '@milkdown/plugin-listener';
import { history } from '@milkdown/kit/plugin/history';

const editors = new Map();

async function create(containerId, initialMarkdown = '') {
    // Destroy existing instance if any
    if (editors.has(containerId)) {
        await destroy(containerId);
    }

    const container = document.getElementById(containerId);
    if (!container) {
        throw new Error(`Container #${containerId} not found`);
    }

    // Clear any previous content
    container.innerHTML = '';

    const editor = await Editor.make()
        .config((ctx) => {
            ctx.set(rootCtx, container);
            ctx.set(defaultValueCtx, initialMarkdown);
            ctx.get(listenerCtx).markdownUpdated((_ctx, markdown, prevMarkdown) => {
                if (markdown !== prevMarkdown) {
                    document.dispatchEvent(new CustomEvent('milkdown:change', {
                        detail: { editorId: containerId, markdown }
                    }));
                }
            });
        })
        .use(commonmark)
        .use(history)
        .use(listener)
        .create();

    editors.set(containerId, editor);
    return containerId;
}

function getMarkdownContent(editorId) {
    const editor = editors.get(editorId);
    if (!editor) return '';
    return editor.action(getMarkdown());
}

function setMarkdownContent(editorId, value) {
    const editor = editors.get(editorId);
    if (!editor) return;
    editor.action(replaceAll(value));
}

async function destroy(editorId) {
    const editor = editors.get(editorId);
    if (!editor) return;
    await editor.destroy();
    editors.delete(editorId);
    const container = document.getElementById(editorId);
    if (container) container.innerHTML = '';
}

function isReady(editorId) {
    return editors.has(editorId);
}

// Execute a formatting command using ProseMirror's schema and commands directly.
// This is more reliable than Milkdown's callCommand which depends on specific exports.
function runEditorCommand(editorId, commandName, payload) {
    const editor = editors.get(editorId);
    if (!editor) return false;
    try {
        editor.action((ctx) => {
            const view = ctx.get(editorViewCtx);
            const { state, dispatch } = view;
            const schema = state.schema;

            switch (commandName) {
                case 'bold': {
                    const mark = schema.marks.strong;
                    if (!mark) break;
                    toggleMark(mark)(state, dispatch);
                    break;
                }
                case 'italic': {
                    const mark = schema.marks.emphasis;
                    if (!mark) break;
                    toggleMark(mark)(state, dispatch);
                    break;
                }
                case 'code': {
                    const mark = schema.marks.inlineCode || schema.marks.code_inline;
                    if (!mark) break;
                    toggleMark(mark)(state, dispatch);
                    break;
                }
                case 'link': {
                    const mark = schema.marks.link;
                    if (!mark) break;
                    const { from, to } = state.selection;
                    if (from === to) break; // Need selection for link
                    const href = payload?.href || '';
                    if (!href) break;
                    const tr = state.tr.addMark(from, to, mark.create({ href }));
                    dispatch(tr);
                    break;
                }
                case 'heading': {
                    const nodeType = schema.nodes.heading;
                    if (!nodeType) break;
                    setBlockType(nodeType, { level: 2 })(state, dispatch);
                    break;
                }
                case 'quote': {
                    const nodeType = schema.nodes.blockquote;
                    if (!nodeType) break;
                    wrapIn(nodeType)(state, dispatch);
                    break;
                }
                case 'bullet': {
                    const nodeType = schema.nodes.bullet_list || schema.nodes.bulletList;
                    if (!nodeType) break;
                    wrapInList(nodeType)(state, dispatch);
                    break;
                }
                case 'ordered': {
                    const nodeType = schema.nodes.ordered_list || schema.nodes.orderedList;
                    if (!nodeType) break;
                    wrapInList(nodeType)(state, dispatch);
                    break;
                }
                case 'divider': {
                    const nodeType = schema.nodes.hr || schema.nodes.horizontal_rule;
                    if (!nodeType) break;
                    const { $from } = state.selection;
                    const tr = state.tr.replaceSelectionWith(nodeType.create());
                    dispatch(tr);
                    break;
                }
                case 'paragraph': {
                    const nodeType = schema.nodes.paragraph;
                    if (!nodeType) break;
                    setBlockType(nodeType)(state, dispatch);
                    break;
                }
                default:
                    console.warn('Unknown command:', commandName);
            }
            view.focus();
        });
        return true;
    } catch (e) {
        console.warn('Command failed:', commandName, e);
        return false;
    }
}

// ProseMirror command helpers (inlined to avoid extra imports)
function toggleMark(markType, attrs) {
    return function(state, dispatch) {
        const { from, to } = state.selection;
        if (state.doc.rangeHasMark(from, to, markType)) {
            if (dispatch) dispatch(state.tr.removeMark(from, to, markType));
        } else {
            if (dispatch) dispatch(state.tr.addMark(from, to, markType.create(attrs)));
        }
        return true;
    };
}

function setBlockType(nodeType, attrs) {
    return function(state, dispatch) {
        const { $from, $to } = state.selection;
        let applicable = false;
        state.doc.nodesBetween($from.pos, $to.pos, (node, pos) => {
            if (applicable) return false;
            if (!node.isTextblock || node.hasMarkup(nodeType, attrs)) return;
            if (node.type === nodeType) {
                // Already this type — toggle back to paragraph
                applicable = true;
            } else {
                applicable = true;
            }
        });
        if (!applicable) return false;
        if (dispatch) {
            const tr = state.tr;
            state.doc.nodesBetween($from.pos, $to.pos, (node, pos) => {
                if (node.isTextblock) {
                    tr.setNodeMarkup(pos, nodeType, attrs);
                }
            });
            dispatch(tr.scrollIntoView());
        }
        return true;
    };
}

function wrapIn(nodeType) {
    return function(state, dispatch) {
        const { $from, $to } = state.selection;
        const range = $from.blockRange($to);
        if (!range) return false;
        const wrapping = state.doc.resolve(range.start).blockRange(state.doc.resolve(range.end));
        if (!wrapping) return false;
        if (dispatch) {
            const tr = state.tr.wrap(range, [{ type: nodeType }]);
            dispatch(tr.scrollIntoView());
        }
        return true;
    };
}

function wrapInList(nodeType) {
    return function(state, dispatch) {
        const { $from, $to } = state.selection;
        const range = $from.blockRange($to);
        if (!range) return false;
        if (dispatch) {
            const tr = state.tr.wrap(range, [
                { type: nodeType },
                { type: state.schema.nodes.list_item || state.schema.nodes.listItem },
            ]);
            dispatch(tr.scrollIntoView());
        }
        return true;
    };
}

// Delete n characters before cursor using ProseMirror transaction
function deleteBeforeCursor(editorId, count = 1) {
    const editor = editors.get(editorId);
    if (!editor) return false;
    try {
        editor.action((ctx) => {
            const view = ctx.get(editorViewCtx);
            const { state } = view;
            const { from } = state.selection;
            if (from < count) return;
            const tr = state.tr.delete(from - count, from);
            view.dispatch(tr);
        });
        return true;
    } catch (e) {
        console.warn('deleteBeforeCursor failed:', e);
        return false;
    }
}

window.MilkdownBridge = {
    create,
    getMarkdown: getMarkdownContent,
    setMarkdown: setMarkdownContent,
    destroy,
    isReady,
    runCommand: runEditorCommand,
    deleteBeforeCursor,
};

window.dispatchEvent(new Event('milkdown:ready'));
