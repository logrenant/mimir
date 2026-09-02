import { useCallback, useRef, useState } from "react";
import { api, DaemonError, type Attachment } from "../lib/daemon";
import { dataURIFor, imageFilesFrom, prepareFile } from "../lib/attachments";

/**
 * Writing a task, images included.
 *
 * Three ways an image gets in — paste, drag-and-drop, and a file input — and
 * all three hand back a `File`, so none of them needs a Tauri plugin, a
 * filesystem permission or a Rust command. Drag-and-drop needs
 * `"dragDropEnabled": false` on the window: Tauri's native handler otherwise
 * swallows the event and gives back a path this WebView could not read anyway.
 *
 * The preview is a data: URI because the app's CSP allows those and does not
 * allow an http://127.0.0.1 image.
 */

export type Attached = Attachment & { previewURI: string };

export type ComposedTask = { title: string; prompt: string; attachmentIDs: string[] };

export function TaskComposer({
  prompt,
  onPromptChange,
  title,
  onTitleChange,
  attachments,
  onAttachmentsChange,
  placeholder,
  rows = 5,
  onSubmit,
  disabled,
}: {
  prompt: string;
  onPromptChange: (value: string) => void;
  title?: string;
  onTitleChange?: (value: string) => void;
  attachments: Attached[];
  onAttachmentsChange: (next: Attached[]) => void;
  placeholder?: string;
  rows?: number;
  /** Called on ⌘/Ctrl+Enter, when the parent wants a keyboard send. */
  onSubmit?: () => void;
  disabled?: boolean;
}) {
  const [uploading, setUploading] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const [dragging, setDragging] = useState(false);
  const picker = useRef<HTMLInputElement>(null);

  const addFiles = useCallback(
    async (files: File[]) => {
      if (files.length === 0) return;
      setUploading(true);
      setProblem(null);

      const added: Attached[] = [];
      for (const file of files) {
        try {
          const prepared = await prepareFile(file);
          const stored = await api.uploadAttachment(prepared.filename, prepared.dataBase64);
          added.push({ ...stored, previewURI: prepared.dataURI });
        } catch (err) {
          // Whatever else was dropped alongside it still gets attached: one bad
          // file is not a reason to lose the other three.
          setProblem(err instanceof DaemonError ? err.message : String((err as Error).message ?? err));
        }
      }
      if (added.length > 0) onAttachmentsChange([...attachments, ...added]);
      setUploading(false);
    },
    [attachments, onAttachmentsChange],
  );

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 9 }}>
      {onTitleChange && (
        <input
          value={title ?? ""}
          onChange={(e) => onTitleChange(e.target.value)}
          placeholder="Başlık (isteğe bağlı)"
          disabled={disabled}
          style={{
            background: "#101114",
            border: "1px solid #24272d",
            borderRadius: 6,
            padding: "8px 10px",
            font: "450 12.5px/1 ui-sans-serif,system-ui",
            color: "#eef0f2",
            outline: "none",
          }}
        />
      )}

      <textarea
        value={prompt}
        rows={rows}
        disabled={disabled}
        onChange={(e) => onPromptChange(e.target.value)}
        onPaste={(e) => {
          const images = imageFilesFrom(e.clipboardData);
          if (images.length === 0) return;
          e.preventDefault();
          void addFiles(images);
        }}
        onDragOver={(e) => {
          e.preventDefault();
          setDragging(true);
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={(e) => {
          e.preventDefault();
          setDragging(false);
          void addFiles(imageFilesFrom(e.dataTransfer));
        }}
        onKeyDown={(e) => {
          if (onSubmit && e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
            e.preventDefault();
            onSubmit();
          }
        }}
        placeholder={placeholder ?? "Bu klasörde ne yapılsın? Görsel yapıştırabilir veya sürükleyebilirsiniz."}
        style={{
          background: "#101114",
          border: `1px solid ${dragging ? "#2547e8" : "#24272d"}`,
          borderRadius: 6,
          padding: "10px 11px",
          font: "400 12.5px/1.6 ui-sans-serif,system-ui",
          color: "#eef0f2",
          outline: "none",
          resize: "vertical",
        }}
      />

      <div style={{ display: "flex", alignItems: "center", gap: 9, flexWrap: "wrap" }}>
        <input
          ref={picker}
          type="file"
          accept="image/png,image/jpeg,image/gif,image/webp"
          multiple
          style={{ display: "none" }}
          onChange={(e) => {
            void addFiles(Array.from(e.target.files ?? []));
            e.target.value = "";
          }}
        />
        <button
          type="button"
          disabled={disabled || uploading}
          onClick={() => picker.current?.click()}
          style={{
            background: "none",
            border: "1px solid #24272d",
            borderRadius: 5,
            padding: "4px 9px",
            cursor: disabled || uploading ? "default" : "pointer",
            font: "400 10.5px/1 ui-monospace,Menlo,monospace",
            color: "#8a9099",
          }}
        >
          {uploading ? "yükleniyor…" : "görsel ekle"}
        </button>

        {attachments.map((att) => (
          <span
            key={att.id}
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: 6,
              border: "1px solid #24272d",
              borderRadius: 5,
              padding: "3px 6px 3px 3px",
              background: "#16181c",
            }}
          >
            <img
              src={att.previewURI}
              alt={att.filename}
              style={{ width: 22, height: 22, objectFit: "cover", borderRadius: 3, display: "block" }}
            />
            <span style={{ font: "400 10.5px/1 ui-monospace,Menlo,monospace", color: "#8a9099", maxWidth: 120, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
              {att.filename || att.id.slice(0, 8)}
            </span>
            <button
              type="button"
              onClick={() => onAttachmentsChange(attachments.filter((a) => a.id !== att.id))}
              style={{ background: "none", border: "none", cursor: "pointer", color: "#6b7079", font: "400 12px/1 ui-monospace,Menlo,monospace", padding: 0 }}
            >
              ×
            </button>
          </span>
        ))}
      </div>

      {problem && (
        <p style={{ margin: 0, font: "400 11px/1.5 ui-monospace,Menlo,monospace", color: "#e5484d" }}>{problem}</p>
      )}
    </div>
  );
}

/** Rebuilds a stored attachment's preview from the base64 the daemon returns. */
export async function loadAttachments(ids: string[]): Promise<Attached[]> {
  const loaded = await Promise.all(
    ids.map(async (id) => {
      try {
        const att = await api.getAttachment(id);
        return { ...att, previewURI: dataURIFor(att.mime, att.data_base64 ?? "") };
      } catch {
        return null;
      }
    }),
  );
  return loaded.filter((a): a is Attached => a !== null);
}
