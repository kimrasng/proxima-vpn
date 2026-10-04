import { createBrowserRouter, Navigate } from "react-router-dom";
import NotFound from "./pages/NotFound";
import AdminLayout from "./layouts/AdminLayout";
import UserLayout from "./layouts/UserLayout";
import AuthLayout from "./layouts/AuthLayout";
import AdminAuthGuard from "./components/AdminAuthGuard";
import UserAuthGuard from "./components/UserAuthGuard";

import Dashboard from "./pages/admin/Dashboard";
import Alerts from "./pages/admin/Alerts";
import Activity from "./pages/admin/Activity";
import Nodes from "./pages/admin/Nodes";
import NodeGroups from "./pages/admin/NodeGroups";
import NodeChains from "./pages/admin/NodeChains";
import Plans from "./pages/admin/Plans";
import Promotions from "./pages/admin/Promotions";
import Users from "./pages/admin/Users";
import UserDetail from "./pages/admin/UserDetail";
import Orders from "./pages/admin/Orders";
import OrderDetail from "./pages/admin/OrderDetail";
import AdminAnnouncements from "./pages/admin/Announcements";
import Settings from "./pages/admin/Settings";
import TwoFactor from "./pages/admin/TwoFactor";
import NodeInbounds from "./pages/admin/NodeInbounds";
import NodeDetail from "./pages/admin/NodeDetail";
import UUIDEvictions from "./pages/admin/UUIDEvictions";
import Connections from "./pages/admin/Connections";
import SubscriptionDomains from "./pages/admin/SubscriptionDomains";

import Login from "./pages/auth/Login";
import AdminLogin from "./pages/auth/AdminLogin";
import Register from "./pages/auth/Register";

import Devices from "./pages/user/Devices";
import UserDashboard from "./pages/user/Dashboard";
import UserNodes from "./pages/user/Nodes";
import Traffic from "./pages/user/Traffic";
import PlanInfo from "./pages/user/PlanInfo";
import AccountSettings from "./pages/user/AccountSettings";
import UserAnnouncements from "./pages/user/Announcements";

export const router = createBrowserRouter([
  {
    path: "/",
    element: <Navigate to="/portal/dashboard" replace />,
  },
  {
    element: <AuthLayout />,
    children: [
      { path: "/login", element: <Login /> },
      { path: "/register", element: <Register /> },
      { path: "/admin/login", element: <AdminLogin /> },
    ],
  },
  {
    element: (
      <AdminAuthGuard>
        <AdminLayout />
      </AdminAuthGuard>
    ),
    children: [
      { path: "/admin/dashboard", element: <Dashboard /> },
      { path: "/admin/alerts", element: <Alerts /> },
      { path: "/admin/activity", element: <Activity /> },
      { path: "/admin/nodes", element: <Nodes /> },
      { path: "/admin/nodes/:nodeId", element: <NodeDetail /> },
      { path: "/admin/nodes/:nodeId/inbounds", element: <NodeInbounds /> },
      { path: "/admin/node-groups", element: <NodeGroups /> },
      { path: "/admin/node-chains", element: <NodeChains /> },
      { path: "/admin/plans", element: <Plans /> },
      { path: "/admin/users", element: <Users /> },
      { path: "/admin/users/:userId", element: <UserDetail /> },
      { path: "/admin/uuid-evictions", element: <UUIDEvictions /> },
      { path: "/admin/connections", element: <Connections /> },
      { path: "/admin/subscription-domains", element: <SubscriptionDomains /> },
      { path: "/admin/orders", element: <Orders /> },
      { path: "/admin/orders/:orderId", element: <OrderDetail /> },
      { path: "/admin/promotions", element: <Promotions /> },
      { path: "/admin/announcements", element: <AdminAnnouncements /> },
      { path: "/admin/settings", element: <Settings /> },
      { path: "/admin/2fa", element: <TwoFactor /> },
    ],
  },
  {
    element: (
      <UserAuthGuard>
        <UserLayout />
      </UserAuthGuard>
    ),
    children: [
      { path: "/portal/dashboard", element: <UserDashboard /> },
      { path: "/portal/devices", element: <Devices /> },
      { path: "/portal/nodes", element: <UserNodes /> },
      { path: "/portal/traffic", element: <Traffic /> },
      { path: "/portal/plan", element: <PlanInfo /> },
      { path: "/portal/account", element: <AccountSettings /> },
      { path: "/portal/announcements", element: <UserAnnouncements /> },
    ],
  },
  {
    path: "*",
    element: <NotFound />,
  },
]);
