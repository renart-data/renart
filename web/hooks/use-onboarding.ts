"use client";

import { useAtom } from "jotai";
import { useCallback, useEffect } from "react";

import { onboardingAtom, OnboardingRect } from "@/lib/atoms/onboarding";

const DISMISSED_KEY = "renart-quickstart-tour-dismissed";
const ENVIRONMENT_KEY = "renart-quickstart-tour-environments";
const SPOTLIGHT_CLASS_SELECTOR = ".quickstart-tour-spotlight";
const CARD_WIDTH = 360;
const CARD_HEIGHT_ESTIMATE = 210;
const GAP = 20;
const EDGE_PADDING = 16;

export function useOnboarding(options?: {
  spotlightSelectors?: string[];
  spotlightActive?: boolean;
}) {
  const [state, setState] = useAtom(onboardingAtom);
  const spotlightSelectorsKey = JSON.stringify(options?.spotlightSelectors ?? []);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }

    setState((current) => {
      if (current.tour.quickstartStep !== null) {
        return current;
      }

      return {
        ...current,
        tour: {
          ...current.tour,
          quickstartStep:
            window.localStorage.getItem(DISMISSED_KEY) === "true" ? null : 0,
          environmentStepActive:
            window.localStorage.getItem(ENVIRONMENT_KEY) === "true",
        },
      };
    });
  }, [setState]);

  useEffect(() => {
    if (!options) {
      return;
    }

    setState((current) => ({
      ...current,
      tour: {
        ...current.tour,
        spotlightSelectors: options.spotlightActive
          ? JSON.parse(spotlightSelectorsKey)
          : [],
      },
    }));
  }, [options?.spotlightActive, setState, spotlightSelectorsKey]);

  useEffect(() => {
    if (!state.tour.overlayVisible && state.tour.spotlightSelectors.length === 0) {
      return;
    }

    let frame = 0;
    const interval = window.setInterval(updateGeometry, 100);

    function updateGeometry() {
      window.cancelAnimationFrame(frame);
      frame = window.requestAnimationFrame(() => {
        const rect = findSpotlightRect(state.tour.spotlightSelectors);
        setState((current) => ({
          ...current,
          tour: {
            ...current.tour,
            spotlightRect: rect,
            cardStyle: computeCardStyle(rect),
          },
        }));
      });
    }

    updateGeometry();
    window.addEventListener("resize", updateGeometry);
    window.addEventListener("scroll", updateGeometry, true);

    return () => {
      window.clearInterval(interval);
      window.cancelAnimationFrame(frame);
      window.removeEventListener("resize", updateGeometry);
      window.removeEventListener("scroll", updateGeometry, true);
    };
  }, [setState, state.tour.overlayVisible, state.tour.spotlightSelectors]);

  const setQuickstartStep = useCallback(
    (quickstartStep: number | null) => {
      setState((current) => ({
        ...current,
        tour: { ...current.tour, quickstartStep },
      }));
    },
    [setState]
  );

  const dismissQuickstartTour = useCallback(() => {
    window.localStorage.setItem(DISMISSED_KEY, "true");
    setQuickstartStep(null);
  }, [setQuickstartStep]);

  const setEnvironmentStepActive = useCallback(
    (environmentStepActive: boolean) => {
      if (environmentStepActive) {
        window.localStorage.setItem(ENVIRONMENT_KEY, "true");
      } else {
        window.localStorage.removeItem(ENVIRONMENT_KEY);
      }
      setState((current) => ({
        ...current,
        tour: { ...current.tour, environmentStepActive },
      }));
    },
    [setState]
  );

  const pulseOverlay = useCallback(() => {
    setState((current) => ({
      ...current,
      tour: { ...current.tour, overlayVisible: true },
    }));

    window.setTimeout(() => {
      setState((current) => ({
        ...current,
        tour: { ...current.tour, overlayVisible: false },
      }));
    }, 3000);
  }, [setState]);

  return {
    ...state.tour,
    dismissQuickstartTour,
    pulseOverlay,
    setEnvironmentStepActive,
    setQuickstartStep,
  };
}

function findSpotlightRect(selectors: string[]): OnboardingRect | null {
  const selectorTargets = selectors.flatMap((selector) =>
    Array.from(document.querySelectorAll<HTMLElement>(selector))
  );
  const targets = (selectorTargets.length > 0
    ? selectorTargets
    : Array.from(document.querySelectorAll<HTMLElement>(SPOTLIGHT_CLASS_SELECTOR))
  ).filter((element) => {
    const box = element.getBoundingClientRect();
    return box.width > 0 && box.height > 0;
  });

  if (targets.length === 0) {
    return null;
  }

  const boxes = targets.map((element) => element.getBoundingClientRect());
  const left = Math.min(...boxes.map((box) => box.left));
  const top = Math.min(...boxes.map((box) => box.top));
  const right = Math.max(...boxes.map((box) => box.right));
  const bottom = Math.max(...boxes.map((box) => box.bottom));

  return { left, top, width: right - left, height: bottom - top };
}

function computeCardStyle(rect: OnboardingRect | null) {
  const width = Math.min(CARD_WIDTH, window.innerWidth - EDGE_PADDING * 2);
  if (!rect) {
    return { left: EDGE_PADDING, top: EDGE_PADDING, width };
  }

  const centeredTop = clamp(
    rect.top + rect.height / 2 - CARD_HEIGHT_ESTIMATE / 2,
    EDGE_PADDING,
    window.innerHeight - CARD_HEIGHT_ESTIMATE - EDGE_PADDING
  );
  const centeredLeft = clamp(
    rect.left + rect.width / 2 - width / 2,
    EDGE_PADDING,
    window.innerWidth - width - EDGE_PADDING
  );

  const candidates = [
    {
      left: rect.left + rect.width + GAP,
      top: centeredTop,
      fits: rect.left + rect.width + GAP + width <= window.innerWidth - EDGE_PADDING,
    },
    {
      left: rect.left - GAP - width,
      top: centeredTop,
      fits: rect.left - GAP - width >= EDGE_PADDING,
    },
    {
      left: centeredLeft,
      top: rect.top + rect.height + GAP,
      fits: rect.top + rect.height + GAP + CARD_HEIGHT_ESTIMATE <= window.innerHeight - EDGE_PADDING,
    },
    {
      left: centeredLeft,
      top: rect.top - GAP - CARD_HEIGHT_ESTIMATE,
      fits: rect.top - GAP - CARD_HEIGHT_ESTIMATE >= EDGE_PADDING,
    },
  ];

  const placement = candidates.find((candidate) => candidate.fits) ?? candidates[2];
  return {
    left: clamp(placement.left, EDGE_PADDING, window.innerWidth - width - EDGE_PADDING),
    top: clamp(
      placement.top,
      EDGE_PADDING,
      Math.max(EDGE_PADDING, window.innerHeight - CARD_HEIGHT_ESTIMATE - EDGE_PADDING)
    ),
    width,
  };
}

function clamp(value: number, min: number, max: number) {
  return Math.min(Math.max(value, min), max);
}
