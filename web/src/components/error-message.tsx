import { errorMessage } from "@/lib/errors";

/** Renders the API's own error message (never a generic one). */
export function ErrorMessage({ error }: { error: unknown }) {
  if (!error) return null;
  return (
    <div role="alert" className="rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-sm text-destructive">
      {errorMessage(error)}
    </div>
  );
}
