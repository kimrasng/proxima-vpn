// Each route is loaded on demand rather than shipping every admin and portal
// screen to users before they sign in. Shared UI/i18n still loads normally.
import { lazy } from "react";
import { createBrowserRouter, Navigate } from "react-router-dom";
import AdminAuthGuard from "./components/AdminAuthGuard";
import UserAuthGuard from "./components/UserAuthGuard";

const NotFound = lazy(() => import("./pages/NotFound"));
const AdminLayout = lazy(() => import("./layouts/AdminLayout"));
const UserLayout = lazy(() => import("./layouts/UserLayout"));
const AuthLayout = lazy(() => import("./layouts/AuthLayout"));

const Dashboard = lazy(() => import("./pages/admin/Dashboard"));
const Alerts = lazy(() => import("./pages/admin/Alerts"));
const Activity = lazy(() => import("./pages/admin/Activity"));
const Nodes = lazy(() => import("./pages/admin/Nodes"));
const NodeGroups = lazy(() => import("./pages/admin/NodeGroups"));
const NodeChains = lazy(() => import("./pages/admin/NodeChains"));
const Plans = lazy(() => import("./pages/admin/Plans"));
const Promotions = lazy(() => import("./pages/admin/Promotions"));
const Users = lazy(() => import("./pages/admin/Users"));
const UserDetail = lazy(() => import("./pages/admin/UserDetail"));
const Orders = lazy(() => import("./pages/admin/Orders"));
const OrderDetail = lazy(() => import("./pages/admin/OrderDetail"));
const AdminAnnouncements = lazy(() => import("./pages/admin/Announcements"));
const Settings = lazy(() => import("./pages/admin/Settings"));
const TwoFactor = lazy(() => import("./pages/admin/TwoFactor"));
const NodeInbounds = lazy(() => import("./pages/admin/NodeInbounds"));
const NodeDetail = lazy(() => import("./pages/admin/NodeDetail"));
const UUIDEvictions = lazy(() => import("./pages/admin/UUIDEvictions"));
const Connections = lazy(() => import("./pages/admin/Connections"));
const SubscriptionDomains = lazy(() => import("./pages/admin/SubscriptionDomains"));

const Login = lazy(() => import("./pages/auth/Login"));
const AdminLogin = lazy(() => import("./pages/auth/AdminLogin"));
const Register = lazy(() => import("./pages/auth/Register"));

const Devices = lazy(() => import("./pages/user/Devices"));
const UserDashboard = lazy(() => import("./pages/user/Dashboard"));
const UserNodes = lazy(() => import("./pages/user/Nodes"));
const Traffic = lazy(() => import("./pages/user/Traffic"));
const PlanInfo = lazy(() => import("./pages/user/PlanInfo"));
const AccountSettings = lazy(() => import("./pages/user/AccountSettings"));
const UserAnnouncements = lazy(() => import("./pages/user/Announcements"));

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
