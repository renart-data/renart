import { useNavigate, useRouter } from "@tanstack/react-router";

// A blocked route transition is not a tool selection. In particular, Stay in
// a dirty editor must not switch the rail/tab or close its contextual sidebar.
export function useToolNavigation() {
  const navigate = useNavigate();
  const router = useRouter();
  return async (to: string) => {
    const before = router.state.location.href;
    const destination = router.buildLocation({ to: to as never });
    await navigate({ to: to as never });
    const after = router.state.location.href;
    return after === destination.href || after !== before;
  };
}
