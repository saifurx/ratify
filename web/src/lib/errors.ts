// Errors surface as toasts carrying the API's own message, never a generic
// "Something went wrong".
export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    public code?: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  if (err instanceof TypeError) return "Network error: the server could not be reached. Check your connection and try again.";
  if (err instanceof Error && err.message) return err.message;
  return "The request failed and the server returned no details.";
}

/** Reads the API's {"error": "...", "code": "..."} body from a failed response. */
export async function toApiError(res: Response): Promise<ApiError> {
  let message = `Request failed with status ${res.status}`;
  let code: string | undefined;
  try {
    const body = await res.json();
    if (typeof body?.error === "string") message = body.error;
    if (typeof body?.code === "string") code = body.code;
  } catch {
    /* non-JSON body: keep the status message */
  }
  return new ApiError(res.status, message, code);
}
