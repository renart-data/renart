import { Link, Outlet } from "@tanstack/react-router";
import { CalendarClock, GanttChart, Package } from "lucide-react";

import { Button } from "@/components/ui/button";

const scheduleSections = [
  { to: "/schedules", label: "Schedules", icon: CalendarClock },
  { to: "/schedules/deployments", label: "Deployments", icon: Package },
  { to: "/schedules/timeline", label: "Run timeline", icon: GanttChart },
] as const;

export function AppSchedulesLayout() {
  return (
    <div className="flex h-full min-h-0 flex-col bg-muted/40">
      <nav
        aria-label="Schedule operations"
        className="flex h-10 shrink-0 items-center gap-1 border-b bg-background px-3"
      >
        {scheduleSections.map(({ to, label, icon: Icon }) => (
          <Button key={to} asChild size="sm" variant="ghost">
            <Link
              to={to}
              activeOptions={{ exact: true }}
              activeProps={{ className: "bg-muted text-foreground" }}
            >
              <Icon data-icon="inline-start" />
              {label}
            </Link>
          </Button>
        ))}
      </nav>
      <div className="min-h-0 flex-1">
        <Outlet />
      </div>
    </div>
  );
}
