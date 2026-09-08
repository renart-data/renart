import { Skeleton } from "@/components/ui/skeleton";

export function DataBrowserLoading({ label, table = false }: { label: string; table?: boolean }) {
  return (
    <div
      role="status"
      aria-busy="true"
      className="flex flex-col gap-3 p-3"
      data-testid="data-browser-loading"
    >
      <span className="sr-only">{label}</span>
      <div aria-hidden="true" className="flex flex-col gap-3">
        {Array.from({ length: 5 }, (_, index) =>
          table ? (
            <div key={index} className="grid grid-cols-3 gap-4 py-1">
              <Skeleton className="h-3 w-4/5 motion-reduce:animate-none" />
              <Skeleton className="h-3 w-3/5 motion-reduce:animate-none" />
              <Skeleton className="h-3 w-2/3 motion-reduce:animate-none" />
            </div>
          ) : (
            <div key={index} className="flex items-center gap-2">
              <Skeleton className="size-7 shrink-0 motion-reduce:animate-none" />
              <div className="flex min-w-0 flex-1 flex-col gap-1.5">
                <Skeleton className="h-3 w-3/4 motion-reduce:animate-none" />
                <Skeleton className="h-2 w-1/2 motion-reduce:animate-none" />
              </div>
            </div>
          ),
        )}
      </div>
    </div>
  );
}
