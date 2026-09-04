import { Markdown } from "@tiptap/markdown";
import { EditorContent, useEditor } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import { useEffect } from "react";

interface Props {
  content: string;
  maxHeight?: string;
}

export function MarkdownViewer({ content, maxHeight }: Props) {
  const editor = useEditor({
    extensions: [StarterKit, Markdown],
    content,
    contentType: "markdown",
    editable: false,
    editorProps: {
      attributes: {
        class: "tiptap-readonly",
      },
    },
  });

  useEffect(() => {
    if (editor && content) {
      editor.commands.setContent(content, { contentType: "markdown" });
    }
  }, [editor, content]);

  return (
    <div
      className="markdown-viewer"
      style={{ maxHeight, overflow: maxHeight ? "auto" : undefined }}
    >
      <EditorContent editor={editor} />
    </div>
  );
}
