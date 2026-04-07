import { useEditor, EditorContent } from '@tiptap/react';
import StarterKit from '@tiptap/starter-kit';
import { Markdown } from 'tiptap-markdown';
import { useEffect } from 'react';

interface Props {
  content: string;
  maxHeight?: string;
}

export function MarkdownViewer({ content, maxHeight }: Props) {
  const editor = useEditor({
    extensions: [StarterKit, Markdown],
    content,
    editable: false,
    editorProps: {
      attributes: {
        class: 'tiptap-readonly',
      },
    },
  });

  useEffect(() => {
    if (editor && content) {
      editor.commands.setContent(content);
    }
  }, [editor, content]);

  return (
    <div
      className="markdown-viewer"
      style={{ maxHeight, overflow: maxHeight ? 'auto' : undefined }}
    >
      <EditorContent editor={editor} />
    </div>
  );
}
