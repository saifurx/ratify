import Link from "next/link";

export default function NotFound() {
  return (
    <div className="mx-auto max-w-md py-24 text-center">
      <h1 className="text-xl font-semibold">Not found</h1>
      <p className="mt-2 text-sm text-muted-foreground">This record does not exist, or you do not have access to it.</p>
      <Link href="/" className="mt-4 inline-block text-sm text-primary underline">
        Back to the dashboard
      </Link>
    </div>
  );
}
