import type { ComponentProps } from "react";
import { useNavigationArrivalRef } from "@/hooks/use-navigation-arrival";
import { cn } from "@/lib/utils";

// Presentation only: destinations still belong to their ordinary routed UI.
export function NavigationArrivalTarget({
  arrival,
  children,
  className,
  ...props
}: Omit<ComponentProps<"div">, "ref"> & { arrival?: string }) {
  const ref = useNavigationArrivalRef(arrival);
  return (
    <div role="group" tabIndex={-1} {...props} className={cn("outline-none", className)} ref={ref}>
      {children}
    </div>
  );
}
