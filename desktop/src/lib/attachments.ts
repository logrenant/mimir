/**
 * Turning what a browser hands us into what the daemon accepts.
 *
 * Three ways an image arrives — paste, drop, file picker — and all three end at
 * a `File`, which is why none of them needs a Tauri plugin or a filesystem
 * permission. The daemon decides the type from the bytes; the checks here exist
 * to tell the operator before a round trip, not to be trusted instead of it.
 */

/** The same allow-list internal/coderunner enforces on the other side. */
export const ACCEPTED_TYPES = ["image/png", "image/jpeg", "image/gif", "image/webp"];

/** internal/config.CodingAttachmentMaxBytes. */
export const MAX_ATTACHMENT_BYTES = 10 * 1024 * 1024;

export type PreparedFile = { filename: string; dataBase64: string; dataURI: string };

export function validateFile(file: File): string | null {
  if (!ACCEPTED_TYPES.includes(file.type)) {
    return `${file.name}: yalnız PNG, JPEG, GIF ve WebP eklenebilir`;
  }
  if (file.size > MAX_ATTACHMENT_BYTES) {
    return `${file.name}: ${Math.round(file.size / 1024 / 1024)} MB çok büyük (en fazla 10 MB)`;
  }
  return null;
}

/** Base64 without the data: prefix, which is what the upload route wants. */
export function splitDataURI(dataURI: string): string {
  const comma = dataURI.indexOf(",");
  return comma >= 0 ? dataURI.slice(comma + 1) : dataURI;
}

export async function prepareFile(file: File): Promise<PreparedFile> {
  const problem = validateFile(file);
  if (problem) throw new Error(problem);

  const dataURI = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(new Error(`${file.name}: okunamadı`));
    reader.readAsDataURL(file);
  });

  return { filename: file.name, dataBase64: splitDataURI(dataURI), dataURI };
}

/** Every image in a paste or a drop, ignoring whatever else came with it. */
export function imageFilesFrom(transfer: DataTransfer | null): File[] {
  if (!transfer) return [];
  return Array.from(transfer.files).filter((f) => f.type.startsWith("image/"));
}

export function dataURIFor(mime: string, base64: string): string {
  return `data:${mime};base64,${base64}`;
}
